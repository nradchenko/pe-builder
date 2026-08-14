// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package coreimg

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/target"
)

// bootImage is a recognizable 2 KB stand-in for the source's CDBOOT sector — the El Torito
// no-emulation, 2 KB-load shape XP boot needs (4 × 512-byte virtual sectors).
var bootImage = bytes.Repeat([]byte("PEBOOT\x00\x00"), 2048/8)

// TestStageMembers copies exactly the listed members, preserving structure, and errors on a
// member the tree never placed (a would-be missing boot-start file).
func TestStageMembers(t *testing.T) {
	src := writeTree(t, map[string]string{
		"i386/ntoskrnl.exe":               "kernel",
		"i386/system32/drivers/atapi.sys": "driver",
		"i386/system32/shell32.dll":       "shell", // not a member
	})
	dst := t.TempDir()

	members := []string{"i386/ntoskrnl.exe", "i386/system32/drivers/atapi.sys"}
	if err := stageMembers(src, members, dst); err != nil {
		t.Fatalf("stageMembers: %v", err)
	}
	if got := readFile(t, filepath.Join(dst, "i386/system32/drivers/atapi.sys")); got != "driver" {
		t.Errorf("staged driver content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "i386/system32/shell32.dll")); !os.IsNotExist(err) {
		t.Errorf("non-member shell32.dll was staged (err=%v)", err)
	}

	if err := stageMembers(src, []string{"i386/absent.sys"}, t.TempDir()); err == nil {
		t.Error("stageMembers accepted a member absent from the tree; want a hard error")
	}
}

// TestBuild masters a core subset into a gzipped bootable image and verifies, by reopening
// the gunzipped ISO, that it holds exactly the members (uppercased for the boot-readable
// on-disc names) plus a valid El Torito CDBOOT — and none of the non-core tree.
func TestBuild(t *testing.T) {
	srcISO := makeSourceISO(t) // a synthetic bootable source CD to extract the CDBOOT from

	// An assembled PE tree: some files are the boot core, some belong only in the WIM.
	srcTree := writeTree(t, map[string]string{
		"i386/setupldr.bin":                "loader",
		"i386/ntdetect.com":                "detect",
		"i386/txtsetup.sif":                "[SetupData]\r\n",
		"i386/ntoskrnl.exe":                "kernel",
		"i386/system32/drivers/atapi.sys":  "driver",
		"i386/system32/drivers/wimmf.sys":  "overlay",
		"i386/system32/SETUPREG.HIV":       "hive",
		"i386/system32/shell32.dll":        "shell",   // WIM-only
		"i386/system32/notepad.exe":        "notepad", // WIM-only
		"i386/system32/drivers/exotic.sys": "exotic",  // WIM-only
	})
	members := []string{
		"i386/setupldr.bin", "i386/ntdetect.com", "i386/txtsetup.sif",
		"i386/ntoskrnl.exe", "i386/system32/drivers/atapi.sys",
		"i386/system32/drivers/wimmf.sys", "i386/system32/SETUPREG.HIV",
	}

	out := filepath.Join(t.TempDir(), "coreimg.gz")
	f, err := os.Open(srcISO)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Build(context.Background(), srcTree, members, f, Options{OutputPath: out, VolumeLabel: "PECORE"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The artifact must be gzip (grub4dos map --mem gunzips it into RAM).
	iso := gunzip(t, out)

	// The gunzipped image is a bootable El Torito CD whose boot record is the source's CDBOOT.
	rf, err := os.Open(iso)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	img, err := boot.ExtractBootImage(rf)
	if err != nil {
		t.Fatalf("coreimg is not a bootable El Torito ISO: %v", err)
	}
	if !bytes.Equal(img, bootImage) {
		t.Error("coreimg boot image is not the source CDBOOT sector")
	}

	// Exactly the members are present (uppercased, case-sensitively — CDBOOT searches for
	// \I386\SETUPLDR.BIN), and no WIM-only file leaked into the core.
	names := isoTree(t, iso)
	for _, want := range []string{
		"/I386/SETUPLDR.BIN", "/I386/NTOSKRNL.EXE", "/I386/TXTSETUP.SIF",
		"/I386/SYSTEM32/DRIVERS/ATAPI.SYS", "/I386/SYSTEM32/DRIVERS/WIMMF.SYS",
		"/I386/SYSTEM32/SETUPREG.HIV",
	} {
		if !containsExact(names, want) {
			t.Errorf("core member %s missing from coreimg", want)
		}
	}
	for _, unwanted := range []string{
		"/I386/SYSTEM32/SHELL32.DLL", "/I386/SYSTEM32/NOTEPAD.EXE",
		"/I386/SYSTEM32/DRIVERS/EXOTIC.SYS",
	} {
		if containsExact(names, unwanted) {
			t.Errorf("WIM-only file %s leaked into coreimg", unwanted)
		}
	}
	// The boot image and catalog stay hidden from the file tree.
	for _, hidden := range []string{"/BOOTSECT.BIN", "/bootsect.bin", "/BOOT.CAT"} {
		if containsFold(names, hidden) {
			t.Errorf("%s should be hidden from the coreimg tree", hidden)
		}
	}

	// The compressed image carries the tail-padding file.
	pad := "/" + tailPadDir + "/" + tailPadName
	if !containsExact(names, pad) {
		t.Errorf("tail padding %s missing from compressed coreimg", pad)
	}

	// And it is the last real content extent — the bytes the grub4dos gunzip corruption lands
	// in — so no boot file sits in the corruptible tail. Verify by offset, not name order: the
	// pad marker must reach the image tail, with only go-diskfs's trailing zero padding after it
	// (it rounds the image up by up to a block). A real extent written after the pad would leave
	// non-zero bytes here, catching the case name-sort order cannot.
	const block = 2048
	full, err := os.ReadFile(iso)
	if err != nil {
		t.Fatal(err)
	}
	last := bytes.LastIndex(full, tailPadMarker)
	if last < 0 {
		t.Fatal("tail pad marker not found in image")
	}
	end := last + len(tailPadMarker)
	if gap := len(full) - end; gap < 0 || gap > block {
		t.Errorf("tail pad does not reach the image tail: %d bytes follow the last pad marker (want <= one %d-byte block of padding)", gap, block)
	}
	for i := end; i < len(full); i++ {
		if full[i] != 0 {
			t.Fatalf("non-zero byte at offset %d after the tail pad — a real extent follows the pad", i)
		}
	}
}

// makeSourceISO masters a minimal bootable source CD (the CDBOOT sector at its root) via the
// public iso target, so Build can extract a real El Torito boot image from it.
func makeSourceISO(t *testing.T) string {
	t.Helper()
	tree := writeTree(t, map[string]string{"i386/setupldr.bin": "loader"})
	if err := os.WriteFile(filepath.Join(tree, boot.BootSectName), bootImage, 0o644); err != nil {
		t.Fatal(err)
	}
	tgt, ok := target.Get("iso")
	if !ok {
		t.Fatal("iso target not registered")
	}
	out := filepath.Join(t.TempDir(), "source.iso")
	if err := tgt.Emit(context.Background(), tree, target.Options{OutputPath: out, VolumeLabel: "SOURCE"}); err != nil {
		t.Fatalf("master source iso: %v", err)
	}
	return out
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gunzip decompresses src to a sibling .iso and returns its path.
func gunzip(t *testing.T, src string) string {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		t.Fatalf("coreimg is not valid gzip: %v", err)
	}
	out := strings.TrimSuffix(src, ".gz") + ".iso"
	of, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(of, zr); err != nil {
		t.Fatal(err)
	}
	if err := of.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

// isoTree returns the sorted absolute paths of every entry in the ISO, ";1" stripped.
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

func containsExact(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func containsFold(ss []string, s string) bool {
	for _, x := range ss {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
