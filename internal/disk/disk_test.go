// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where the first partition entry sits in an MBR, and where its fields sit inside it.
// The tests read these bytes off the image directly rather than through the library, so
// that what is asserted is what a guest's firmware would actually see.
const (
	partitionEntry1 = 446
	entryTypeOffset = 4
	entryLBAOffset  = 8
	entrySizeOffset = 12
)

// mbrEntry is the first partition entry, read from the image file itself.
type mbrEntry struct {
	Bootable bool
	Type     byte
	StartLBA uint32
	Sectors  uint32
}

func readMBR(t *testing.T, path string) mbrEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	buf := make([]byte, 16)
	if _, err := f.ReadAt(buf, partitionEntry1); err != nil {
		t.Fatalf("read partition entry: %v", err)
	}
	return mbrEntry{
		Bootable: buf[0] == 0x80,
		Type:     buf[entryTypeOffset],
		StartLBA: binary.LittleEndian.Uint32(buf[entryLBAOffset:]),
		Sectors:  binary.LittleEndian.Uint32(buf[entrySizeOffset:]),
	}
}

// TestCreateGeometry pins the on-disk shape: one partition, a megabyte in, not bootable,
// carrying the type byte the guest is expected to recognise.
//
// The type byte is read off the image rather than compared against our own constant on
// purpose — mbr.Fat16 is 0x04 and mbr.Fat16b is 0x06, and asserting on the constant we
// passed would agree with itself whichever one we chose.
func TestCreateGeometry(t *testing.T) {
	tests := []struct {
		name     string
		size     int64
		fs       FSType
		wantType byte
		wantFS   FSType
	}{
		{"small-fat16", 16 << 20, FSAuto, 0x06, FAT16},
		// Straddle the library's sectors-per-cluster switches, which is where a
		// formatter defect would hide.
		{"under-32M", 30 << 20, FSAuto, 0x06, FAT16},
		{"over-32M", 40 << 20, FSAuto, 0x06, FAT16},
		{"under-128M", 120 << 20, FSAuto, 0x06, FAT16},
		{"over-128M", 160 << 20, FSAuto, 0x06, FAT16},
		{"typical-256M", 256 << 20, FSAuto, 0x06, FAT16},
		// Around the FAT16 ceiling. The rule is applied to the volume, so a 2 GiB disk is
		// still FAT16: its volume is a megabyte smaller.
		{"at-fat16-ceiling", 2 << 30, FSAuto, 0x06, FAT16},
		// One sector past the ceiling is the other side of the boundary. Sampling only
		// below it is what let the nominal-limit defect through.
		{"one-sector-over-the-ceiling", 2<<30 + sectorSize, FSAuto, 0x0c, FAT32},
		// Inside the nominal 2 GiB FAT16 range but past what its cluster count can
		// actually address. Routed by the nominal limit this lands on FAT16 and is then
		// refused; FSAuto has to send it to FAT32 instead.
		{"just-over-what-fat16-addresses", 2<<30 + 1<<20, FSAuto, 0x0c, FAT32},
		{"over-fat16-ceiling", 3 << 30, FSAuto, 0x0c, FAT32},
		// An explicit choice overrides the size rule where the size allows it.
		{"forced-fat32", 128 << 20, FAT32, 0x0c, FAT32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scratch.img")
			if err := Create(path, Spec{Size: tt.size, FS: tt.fs}); err != nil {
				t.Fatalf("Create: %v", err)
			}

			e := readMBR(t, path)
			if e.Type != tt.wantType {
				t.Errorf("partition type = 0x%02x, want 0x%02x", e.Type, tt.wantType)
			}
			if e.StartLBA != partitionStartLBA {
				t.Errorf("partition starts at LBA %d, want %d", e.StartLBA, partitionStartLBA)
			}
			if e.Bootable {
				t.Error("partition is marked bootable; a scratch disk is data")
			}
			wantSectors := uint32(tt.size/sectorSize) - partitionStartLBA
			if e.Sectors != wantSectors {
				t.Errorf("partition is %d sectors, want %d", e.Sectors, wantSectors)
			}

			// The volume has to be readable, and readable as the filesystem intended:
			// the type byte and the actual filesystem must not disagree.
			img, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer img.Close()

			info := img.Info()
			if info.FS != tt.wantFS {
				t.Errorf("volume is %s, want %s", info.FS, tt.wantFS)
			}
			if want := tt.size - alignmentGap; info.VolumeSize != want {
				t.Errorf("VolumeSize = %d, want %d", info.VolumeSize, want)
			}
			if info.DiskSize != tt.size {
				t.Errorf("DiskSize = %d, want %d", info.DiskSize, tt.size)
			}
			if info.Label != DefaultLabel {
				t.Errorf("Label = %q, want %q", info.Label, DefaultLabel)
			}
		})
	}
}

// TestVolumeSizeIsReported guards the status line a caller prints from it: a caller that
// reported the requested size would claim 256 MiB for a volume that is not.
func TestVolumeSizeIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 256 << 20}); err != nil {
		t.Fatal(err)
	}
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()

	if got := FormatSize(img.Info().VolumeSize); got != "255 MiB" {
		t.Errorf("a 256 MiB disk reports a %s volume, want 255 MiB", got)
	}
}

// TestRoundTrip is the host-side half of the verification: what we write, we can read
// back through a fresh open. It proves the plumbing; only a guest can prove compatibility.
func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 32 << 20}); err != nil {
		t.Fatal(err)
	}

	want := []byte("hello from the host\r\n")
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := img.FS().OpenFile("/hello.txt", os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatalf("OpenFile for write: %v", err)
	}
	if _, err := f.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Close before reading back — the write path buffers, so a read through the same
	// handle would prove less than it appears to.
	if err := img.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	img2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer img2.Close()

	got, err := fs.ReadFile(img2.FS(), "hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("read back %q, want %q", got, want)
	}
}

// TestFSIsAnFSFS is the property the whole design leans on: the volume satisfies the
// standard filesystem interfaces, so it is assignable to source.Backing and the existing
// image-inspection helpers read a disk with no adapter.
func TestFSIsAnFSFS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()

	var backing fs.FS = img.FS()
	if _, ok := backing.(fs.ReadDirFS); !ok {
		t.Error("the volume is not an fs.ReadDirFS")
	}
	if _, ok := backing.(fs.StatFS); !ok {
		t.Error("the volume is not an fs.StatFS")
	}

	// WalkDir is what the listing helper uses; an empty volume must walk cleanly rather
	// than error, or `ls` on a fresh disk would look broken.
	n := 0
	if err := fs.WalkDir(backing, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir on an empty volume: %v", err)
	}
	if n != 0 {
		t.Errorf("a fresh volume holds %d files, want 0", n)
	}
}

// TestCaseInsensitive pins the FAT property the inspection commands rely on, since a
// built image's backing is case-insensitive too and the two must agree.
func TestCaseInsensitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := img.FS().OpenFile("/Hello.TXT", os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "x"); err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}

	img2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img2.Close()
	if _, err := fs.ReadFile(img2.FS(), "hello.txt"); err != nil {
		t.Errorf("reading Hello.TXT as hello.txt: %v", err)
	}
}

// Lookup is what decides whether a name is taken, and it has to answer the way a write
// will: case-insensitively, since fs.Stat compares FAT names exactly and a guest writes
// uppercase 8.3 names.
func TestLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := img.WriteFile("HELLO.TXT", []byte("guest")); err != nil {
		t.Fatal(err)
	}
	if err := img.WriteFile("logs/inner.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}

	img2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img2.Close()

	tests := []struct {
		name      string
		wantFound bool
		wantDir   bool
		wantName  string
	}{
		{"HELLO.TXT", true, false, "HELLO.TXT"},
		{"hello.txt", true, false, "HELLO.TXT"},
		{"/hello.txt", true, false, "HELLO.TXT"},
		{`\hello.txt`, true, false, "HELLO.TXT"},
		{"logs", true, true, "logs"},
		{"LOGS", true, true, "logs"},
		{"logs/inner.txt", true, false, "inner.txt"},
		{"logs/INNER.TXT", true, false, "inner.txt"},
		{"nosuch.txt", false, false, ""},
		{"logs/nosuch.txt", false, false, ""},
		{"nosuchdir/file.txt", false, false, ""},
	}
	for _, tt := range tests {
		got, ok := img2.Lookup(tt.name)
		if ok != tt.wantFound {
			t.Errorf("Lookup(%q) found = %v, want %v", tt.name, ok, tt.wantFound)
			continue
		}
		if !ok {
			continue
		}
		if got.IsDir != tt.wantDir {
			t.Errorf("Lookup(%q).IsDir = %v, want %v", tt.name, got.IsDir, tt.wantDir)
		}
		// The name comes back as the volume spells it, so a caller's message can show a
		// case difference rather than echoing what was typed.
		if !strings.EqualFold(got.Name, tt.wantName) {
			t.Errorf("Lookup(%q).Name = %q, want %q", tt.name, got.Name, tt.wantName)
		}
	}
}

// Writing over a directory corrupts the volume, so it is refused rather than attempted.
func TestWriteFileRefusesADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := Create(path, Spec{Size: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	img, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()

	if err := img.WriteFile("logs/inner.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	err = img.WriteFile("logs", []byte("not a directory"))
	if err == nil {
		t.Fatal("writing over a directory: want an error")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q does not say the target is a directory", err)
	}

	// The volume must still walk — a corrupted directory entry shows up as a walk that
	// never terminates, so reaching this assertion at all is half the check.
	names, werr := fs.ReadDir(img.FS(), "logs")
	if werr != nil {
		t.Fatalf("the directory did not survive: %v", werr)
	}
	if len(names) != 1 {
		t.Errorf("logs holds %d entries, want 1", len(names))
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := os.WriteFile(path, []byte("not a disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Create(path, Spec{Size: 16 << 20})
	if err == nil {
		t.Fatal("Create over an existing file: want an error")
	}
	// The existing file must be exactly as it was: a disk may hold what a guest wrote,
	// and this package never destroys one.
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "not a disk" {
		t.Errorf("the existing file was modified: %q", got)
	}
}

// TestCreateLeavesNoPartialFile covers the failure that matters most: a half-written
// image left behind would later be attached to a boot as though it were whole.
func TestCreateLeavesNoPartialFile(t *testing.T) {
	// A size past the FAT16 ceiling with FAT16 demanded fails in validation, before any
	// file exists; a size the library refuses at format time fails after it does. Both
	// must leave nothing behind.
	dir := t.TempDir()
	for _, tt := range []struct {
		name string
		spec Spec
		// stage is text the error must carry, which pins *where* the failure happened.
		// Without it this test passes whether or not the file was ever created, and the
		// cleanup it exists to check would go untested.
		stage string
	}{
		{"refused-before-create", Spec{Size: 3 << 30, FS: FAT16}, "FAT16 can address"},
		// A size an MBR can still describe but FAT32 cannot fill: the band between the
		// library's own FAT32 maximum and the 32-bit sector ceiling is about 256 MiB
		// wide, and everything in it passes validation and fails at format. The file is
		// sparse, so asking for two terabytes costs nothing but the metadata.
		{"refused-at-format", Spec{Size: maxDisk, FS: FAT32}, "format"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".img")
			err := Create(path, tt.spec)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tt.stage) {
				t.Fatalf("error %q does not show the failure reached the %s stage", err, tt.name)
			}
			if _, serr := os.Stat(path); !os.IsNotExist(serr) {
				t.Errorf("a partial file was left at %s", path)
			}
		})
	}
}

func TestCreateRejects(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{"zero", Spec{Size: 0}, "size is required"},
		{"negative", Spec{Size: -1}, "size is required"},
		{"not-a-sector-multiple", Spec{Size: 16<<20 + 1}, "sectors"},
		{"no-room-for-a-volume", Spec{Size: alignmentGap}, "alignment"},
		// Refused in our own words. fat16.Create would refuse it too (it enforces the
		// 4085-cluster minimum itself), so this is about the message, not about
		// preventing a bad volume.
		{"too-small-for-fat16", Spec{Size: alignmentGap + (2 << 20)}, "too small"},
		{"fat16-too-large", Spec{Size: 3 << 30, FS: FAT16}, "FAT16 can address"},
		// FAT32 enforces no cluster minimum of its own, so this refusal is the only thing
		// standing between a caller and an 0x0c type byte over a volume with far fewer
		// than the 65525 clusters FAT32 is defined by.
		{"too-small-for-fat32", Spec{Size: alignmentGap + (9 << 20), FS: FAT32}, "too small"},
		// Past what a 32-bit sector count can express. Unchecked, the conversion wraps
		// and the disk is silently partitioned at a fraction of its size.
		{"larger-than-an-mbr-can-describe", Spec{Size: 3 << 40}, "an MBR can describe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Create(filepath.Join(t.TempDir(), "scratch.img"), tt.spec)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	t.Run("custom", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scratch.img")
		if err := Create(path, Spec{Size: 16 << 20, Label: "WORK"}); err != nil {
			t.Fatal(err)
		}
		img, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer img.Close()
		if got := img.Info().Label; got != "WORK" {
			t.Errorf("Label = %q, want %q", got, "WORK")
		}
	})

	t.Run("uppercased", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scratch.img")
		if err := Create(path, Spec{Size: 16 << 20, Label: "work"}); err != nil {
			t.Fatal(err)
		}
		img, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer img.Close()
		if got := img.Info().Label; got != "WORK" {
			t.Errorf("Label = %q, want it uppercased to %q", got, "WORK")
		}
	})

	t.Run("refused", func(t *testing.T) {
		for _, label := range []string{
			"THIS LABEL IS TOO LONG",
			"BAD/NAME",
			"BAD.NAME",
			"BAD:NAME",
			"TAB\there",
			// Non-ASCII, and specifically one whose uppercase form is longer than
			// itself: checked after uppercasing, a ten-character label containing this
			// would be refused for a length neither the caller nor the field recognises.
			// It has to be refused as the character it is.
			"GRÖSSE",
			"STRAßE",
		} {
			err := Create(filepath.Join(t.TempDir(), "scratch.img"), Spec{Size: 16 << 20, Label: label})
			if err == nil {
				t.Errorf("Create with label %q: want an error", label)
			}
		}
	})
}

func TestOpenRejects(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		if _, err := Open(filepath.Join(dir, "absent.img")); err == nil {
			t.Error("want an error")
		}
	})

	t.Run("not-a-disk", func(t *testing.T) {
		path := filepath.Join(dir, "text.img")
		if err := os.WriteFile(path, make([]byte, 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "partition table") {
			t.Errorf("error %q does not say the image has no partition table", err)
		}
	})
}

func TestParseFSType(t *testing.T) {
	tests := []struct {
		in   string
		want FSType
	}{
		{"", FSAuto},
		{"auto", FSAuto},
		{"fat16", FAT16},
		{"FAT16", FAT16},
		{"fat32", FAT32},
		{" fat32 ", FAT32},
	}
	for _, tt := range tests {
		got, err := ParseFSType(tt.in)
		if err != nil {
			t.Errorf("ParseFSType(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseFSType(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	for _, in := range []string{"ntfs", "ext4", "fat12", "16"} {
		if _, err := ParseFSType(in); err == nil {
			t.Errorf("ParseFSType(%q): want an error", in)
		}
	}
}
