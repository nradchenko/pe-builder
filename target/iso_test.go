// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/internal/golden"
)

// bootImage is a recognizable 2 KB stand-in for the source's CDBOOT sector.
var bootImage = bytes.Repeat([]byte("PEBOOT\x00\x00"), isoBlockSize/8)

// makeTree materializes a minimal but representative output tree: the boot image at
// the root, the loader/detector at the system root, an extensionless hive, and a
// nested driver — enough to exercise \I386 boot-path resolution, extensionless
// names, and subdirectories.
func makeTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	files := map[string][]byte{
		boot.BootSectName:                 bootImage,
		"i386/setupldr.bin":               []byte("loader"),
		"i386/ntdetect.com":               []byte("detect"),
		"i386/txtsetup.sif":               []byte("[SetupData]\r\n"),
		"i386/system32/config/SYSTEM":     []byte("hive"),
		"i386/system32/drivers/atapi.sys": []byte("driver"),
		"i386/system32/startnet.cmd":      []byte("@echo off\r\n"),
	}
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestISOEmit(t *testing.T) {
	tree := makeTree(t)
	out := filepath.Join(t.TempDir(), "out.iso")

	if err := (iso{}).Emit(context.Background(), tree, Options{OutputPath: out, VolumeLabel: "PEBUILDER"}); err != nil {
		t.Fatal(err)
	}

	// The boot record parses as a no-emulation, 2 KB-load entry whose image is our
	// bootsect bytes — i.e. the El Torito shape XP boot needs.
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if got := elToritoLoadSize(t, f); got != 4 {
		t.Errorf("El Torito load size = %d, want 4 (2 KB)", got)
	}
	img, err := boot.ExtractBootImage(f) // also asserts no-emulation + bootable
	if err != nil {
		t.Fatalf("boot record not readable: %v", err)
	}
	if !bytes.Equal(img, bootImage) {
		t.Error("boot image on disc does not match the source boot sector")
	}

	// The file tree resolves the boot chain and keeps extensionless hive names. The
	// match is case-SENSITIVE: the CDBOOT sector searches for \I386\SETUPLDR.BIN, so
	// a lowercase/mixed-case regression in on-disc naming must fail here.
	names := isoTree(t, out)
	for _, want := range []string{"/I386/SETUPLDR.BIN", "/I386/SYSTEM32/CONFIG/SYSTEM", "/I386/SYSTEM32/DRIVERS/ATAPI.SYS"} {
		if !containsExact(names, want) {
			t.Errorf("%s not found (case-sensitively) in ISO tree", want)
		}
	}
	// The boot image and catalog are hidden from the file tree, in any case.
	for _, hidden := range []string{"/BOOTSECT.BIN", "/bootsect.bin", "/BOOT.CAT", "/boot.catalog"} {
		if contains(names, hidden) {
			t.Errorf("%s should be hidden from the ISO tree", hidden)
		}
	}

	// go-diskfs sizes the image to its volume-space-size, so the artifact carries no
	// trailing padding. Allow at most one trailing block of slack against a future
	// rounding change on the writer side.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := volumeBytes(t, f); info.Size() < want || info.Size() > want+isoBlockSize {
		t.Errorf("iso size = %d, want within one block of volume size %d", info.Size(), want)
	}

	golden.Assert(t, "iso-tree", strings.Join(names, "\n")+"\n")
}

// TestUppercaseTree checks the in-place rename uppercases every component and keeps the
// nested structure intact — deepest-first, so a child renames before its parent.
func TestUppercaseTree(t *testing.T) {
	root := makeTree(t) // mixed/lowercase names, several directories deep
	if err := uppercaseTree(root); err != nil {
		t.Fatal(err)
	}
	var lower []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && d.Name() != strings.ToUpper(d.Name()) {
			lower = append(lower, d.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lower) > 0 {
		t.Errorf("names not uppercased: %v", lower)
	}
	// The deepest file survived the rename under its uppercased path.
	if _, err := os.Stat(filepath.Join(root, "I386", "SYSTEM32", "DRIVERS", "ATAPI.SYS")); err != nil {
		t.Errorf("nested file lost after uppercasing: %v", err)
	}
}

// TestUppercaseTreeCollision confirms two names that differ only in case error rather
// than one silently overwriting the other.
func TestUppercaseTreeCollision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(root, "i386"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"data.dll", "Data.DLL"} {
		if err := os.WriteFile(filepath.Join(root, "i386", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := uppercaseTree(root); err == nil {
		t.Fatal("case-colliding names returned no error")
	}
}

// TestISOEmitReplacesExisting: a successful master overwrites whatever is at the output
// path and leaves no scratch file behind in the output directory.
func TestISOEmitReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iso")
	if err := os.WriteFile(out, []byte("stale image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (iso{}).Emit(context.Background(), makeTree(t), Options{OutputPath: out, VolumeLabel: "PEBUILDER"}); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := boot.ExtractBootImage(f); err != nil {
		t.Errorf("output is not a bootable image after replacing a stale one: %v", err)
	}
	assertOnly(t, dir, "out.iso")
}

// TestISOEmitFailureKeepsPrevious is the point of mastering through a scratch file: a
// master that fails partway must leave the previous image at the output path untouched
// rather than a truncated one that nothing can tell from a good ISO. The failure is a
// real mid-master one — go-diskfs rejects the non-ASCII name while writing the
// directory records, well after the image file has been created and partly written.
func TestISOEmitFailureKeepsPrevious(t *testing.T) {
	tree := makeTree(t)
	if err := os.WriteFile(filepath.Join(tree, "i386", "ünicode.dll"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iso")
	previous := []byte("the previously mastered image")
	if err := os.WriteFile(out, previous, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (iso{}).Emit(context.Background(), tree, Options{OutputPath: out, VolumeLabel: "PEBUILDER"}); err == nil {
		t.Fatal("Emit of an unmasterable tree returned no error")
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("previous image gone after a failed master: %v", err)
	}
	if !bytes.Equal(got, previous) {
		t.Errorf("previous image was overwritten by the failed master (%d bytes)", len(got))
	}
	assertOnly(t, dir, "out.iso")
}

// TestReserveTempSitsBesideDest pins the invariant the atomic publish rests on: the
// scratch file must be on the destination's filesystem, because publishing is a rename.
//
// Nothing above catches this. The two tests either side assert that no scratch file is
// *left*, which is equally true of one created in the wrong place and cleaned up. A
// scratch file under $TMPDIR would work on a machine where /tmp shares a filesystem with
// the output and fail with EXDEV on one where it does not — so the bug would not be in
// the code under test but in which machine ran it, and every build on that machine would
// master a whole image and then fail to publish it.
func TestReserveTempSitsBesideDest(t *testing.T) {
	// reserveTemp unlinks the file before returning, so there is nothing to clean up:
	// what it hands back is a reserved name, not an open file.
	dest := filepath.Join(t.TempDir(), "out.iso")
	tmp, err := reserveTemp(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Dir(tmp), filepath.Dir(dest); got != want {
		t.Errorf("scratch file reserved in %s, want beside the destination in %s\n"+
			"(publishing renames it onto dest; across filesystems that fails with EXDEV)", got, want)
	}
}

// assertOnly checks the directory holds exactly the named entries — no scratch file
// left over from mastering.
func assertOnly(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("output directory holds %v, want %v", got, want)
	}
}

// TestISOMissingBootImage: mastering a tree without bootsect.bin is a clear error.
func TestISOMissingBootImage(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(tree, "i386"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.iso")
	if err := (iso{}).Emit(context.Background(), tree, Options{OutputPath: out}); err == nil {
		t.Fatal("Emit without a boot image returned no error")
	}
}

// TestISOXorriso cross-checks the boot record with xorriso when it is available.
func TestISOXorriso(t *testing.T) {
	xorriso, err := exec.LookPath("xorriso")
	if err != nil {
		t.Skip("xorriso not installed; skipping cross-check")
	}
	tree := makeTree(t)
	out := filepath.Join(t.TempDir(), "out.iso")
	if err := (iso{}).Emit(context.Background(), tree, Options{OutputPath: out, VolumeLabel: "PEBUILDER"}); err != nil {
		t.Fatal(err)
	}
	report, err := exec.Command(xorriso, "-indev", out, "-report_el_torito", "plain").CombinedOutput()
	if err != nil {
		t.Fatalf("xorriso: %v\n%s", err, report)
	}
	s := string(report)
	// The reported boot entry must be no-emulation with a load size of 4.
	if !strings.Contains(s, "none") {
		t.Errorf("xorriso does not report no-emulation:\n%s", s)
	}
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, "boot img") && !strings.Contains(ln, "     4  ") {
			t.Errorf("xorriso boot image not load-size 4:\n%s", ln)
		}
	}
}

// elToritoLoadSize reads the default boot entry's load size (in 512-byte sectors).
func elToritoLoadSize(t *testing.T, r *os.File) uint16 {
	t.Helper()
	rec := make([]byte, isoBlockSize)
	if _, err := r.ReadAt(rec, 0x11*isoBlockSize); err != nil {
		t.Fatal(err)
	}
	catLBA := binary.LittleEndian.Uint32(rec[0x47:0x4B])
	cat := make([]byte, isoBlockSize)
	if _, err := r.ReadAt(cat, int64(catLBA)*isoBlockSize); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint16(cat[0x20+6 : 0x20+8])
}

// volumeBytes returns the primary volume descriptor's volume space size in bytes.
func volumeBytes(t *testing.T, r *os.File) int64 {
	t.Helper()
	pvd := make([]byte, isoBlockSize)
	if _, err := r.ReadAt(pvd, pvdSector*isoBlockSize); err != nil {
		t.Fatal(err)
	}
	return int64(binary.LittleEndian.Uint32(pvd[80:84])) * isoBlockSize
}

// isoTree returns the sorted absolute paths of every entry in the mastered ISO,
// with any ISO9660 ";1" version suffix stripped.
func isoTree(t *testing.T, path string) []string {
	t.Helper()
	disk, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatal(err)
	}
	filesystem, err := disk.GetFilesystem(0)
	if err != nil {
		t.Fatal(err)
	}
	fsys, ok := filesystem.(fs.FS)
	if !ok {
		t.Fatal("iso filesystem is not an fs.FS")
	}
	var names []string
	err = fs.WalkDir(fsys, ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." {
			names = append(names, "/"+strings.TrimSuffix(p, ";1"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

// contains reports case-insensitive membership (used to prove a name is absent in
// any case).
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// containsExact reports exact (case-sensitive) membership.
func containsExact(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
