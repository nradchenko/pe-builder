// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/receipt"
	"github.com/nradchenko/pe-builder/target"
)

// stageWIMCDFixture builds a payload tree with boot-core files, WIM-only files, and a reserved
// boot-chain subtree (grldr + the El Torito image), and returns the tree dir and the core
// member paths (tree-relative) the tail would receive from the assemble stage.
func stageWIMCDFixture(t *testing.T, parent string) (tree string, members []string) {
	t.Helper()
	tree = filepath.Join(parent, "work")
	write := func(rel string, content []byte) {
		p := filepath.Join(tree, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Boot-core payload (goes into coreimg AND the WIM).
	core := map[string]string{
		"i386/setupldr.bin":               "loader",
		"i386/ntoskrnl.exe":               "kernel",
		"i386/system32/drivers/atapi.sys": "driver",
		"i386/system32/drivers/wimmf.sys": "overlay",
		"i386/system32/SETUPREG.HIV":      "hive",
	}
	for rel, c := range core {
		write(rel, []byte(c))
		members = append(members, rel)
	}
	// WIM-only payload (must NOT land in coreimg).
	write("i386/system32/shell32.dll", []byte("shell"))
	// Reserved boot chain (relocated to the ISO media root); grub4dos + its El Torito image.
	write(filepath.Join(wimBootReservedDir, "grldr"), []byte("grldr-stub"))
	write(filepath.Join(wimBootReservedDir, wimBootImage), bytes.Repeat([]byte{0x90}, 2048))
	return tree, members
}

// makeBootableSourceISO masters a minimal bootable source CD (a CDBOOT sector at its root) so
// the tail can extract a real El Torito boot image from it for the coreimg.
func makeBootableSourceISO(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(dir, "i386"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i386", "setupldr.bin"), []byte("loader"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, boot.BootSectName), bytes.Repeat([]byte("PEBOOT\x00\x00"), 2048/8), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt, ok := target.Get("iso")
	if !ok {
		t.Fatal("iso target not registered")
	}
	out := filepath.Join(t.TempDir(), "source.iso")
	if err := tgt.Emit(context.Background(), dir, target.Options{OutputPath: out, VolumeLabel: "SOURCE"}); err != nil {
		t.Fatalf("master source iso: %v", err)
	}
	return out
}

// TestEmitWIMCDISO drives the whole wim-cd media tail on a fixture and inspects the ISO: the
// boot chain, generated menu.lst, coreimg, and WIM land at the right media paths; the coreimg
// gunzips to a bootable cdfs holding exactly the core members; and the outer image is bootable.
func TestEmitWIMCDISO(t *testing.T) {
	parent := t.TempDir()
	tree, members := stageWIMCDFixture(t, parent)
	// Build writes the receipt into the tree before the media stage; simulate that so the tail
	// has one to surface on the media root.
	receiptBody := []byte("pebuild-receipt-fixture\n")
	if err := os.WriteFile(filepath.Join(tree, receipt.FileName), receiptBody, 0o644); err != nil {
		t.Fatal(err)
	}
	src := makeBootableSourceISO(t)
	out := filepath.Join(t.TempDir(), "wimcd.iso")
	m := &manifest.Manifest{
		Layout: manifest.LayoutWIM,
		Source: manifest.Source{Path: src},
		Output: manifest.Output{Target: manifest.TargetISO, Path: out, VolumeLabel: "PEWIMCD"},
	}
	rep := &Report{CoreMembers: members}
	if err := emitWIMCDISO(context.Background(), m, tree, rep); err != nil {
		t.Fatal(err)
	}

	if rep.ISOPath != out || rep.ISOSize <= 0 {
		t.Fatalf("report not populated: %+v", rep)
	}
	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Errorf("payload tree should be removed when KeepWork is false")
	}

	fsys := openISOFS(t, out)
	// The media root carries the loader chain, the generated menu.lst, the boot core, and the
	// payload WIM itself — at the root under a short name, no subdirectory (wimmf reaches it via
	// WimPath, so the WIM needs no fixed on-disc path).
	assertNames(t, fsys, ".", "grldr", menuLstName, coreImgName, wimCDImageName)

	// The build receipt is sealed inside the WIM (it was in the tree); the tail also surfaces it
	// on the media root so `pebuild verify` can read it from a wim-cd ISO exactly as from a
	// classic one. Without this it would report a wim-cd image as carrying no receipt.
	rdata, rerr := fs.ReadFile(fsys, receipt.FileName)
	if rerr != nil {
		rdata, rerr = fs.ReadFile(fsys, strings.ToUpper(receipt.FileName))
	}
	if rerr != nil {
		t.Fatalf("receipt absent from wim-cd media root (verify would find none): %v", rerr)
	}
	if !bytes.Equal(rdata, receiptBody) {
		t.Errorf("media receipt = %q, want %q", rdata, receiptBody)
	}

	// menu.lst maps the boot core into RAM as a CD.
	menu, err := fs.ReadFile(fsys, menuLstName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(menu, []byte("map --mem /"+coreImgName+" (0xff)")) {
		t.Errorf("menu.lst does not map the boot core:\n%s", menu)
	}

	// The boot core gunzips to a bootable cdfs holding exactly the members (uppercased), with
	// no WIM-only file leaked in.
	raw, err := fs.ReadFile(fsys, coreImgName)
	if err != nil {
		t.Fatal(err)
	}
	coreISO := gunzipBytes(t, raw)
	if _, err := boot.ExtractBootImage(bytes.NewReader(coreISO)); err != nil {
		t.Errorf("coreimg is not a bootable El Torito ISO: %v", err)
	}
	coreNames := isoNames(t, coreISO)
	for _, want := range []string{"/I386/NTOSKRNL.EXE", "/I386/SYSTEM32/DRIVERS/WIMMF.SYS", "/I386/SYSTEM32/SETUPREG.HIV"} {
		if !containsStr(coreNames, want) {
			t.Errorf("core member %s missing from coreimg", want)
		}
	}
	if containsStr(coreNames, "/I386/SYSTEM32/SHELL32.DLL") {
		t.Error("WIM-only shell32.dll leaked into coreimg")
	}
}

// TestEmitWIMCDISOEmptyCore: with no resolved boot core, the tail fails clearly rather than
// mastering an unbootable ISO.
func TestEmitWIMCDISOEmptyCore(t *testing.T) {
	tree, _ := stageWIMCDFixture(t, t.TempDir())
	m := &manifest.Manifest{
		Layout: manifest.LayoutWIM,
		Output: manifest.Output{Target: manifest.TargetISO, Path: filepath.Join(t.TempDir(), "o.iso")},
	}
	if err := emitWIMCDISO(context.Background(), m, tree, &Report{}); err == nil {
		t.Fatal("empty CoreMembers should fail the tail")
	}
}

// TestEmitWIMCDISONoReserved: without the boot chain staged, the tail fails clearly.
func TestEmitWIMCDISONoReserved(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Join(tree, "i386"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "i386", "ntoskrnl.exe"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Layout: manifest.LayoutWIM,
		Output: manifest.Output{Target: manifest.TargetISO, Path: filepath.Join(t.TempDir(), "o.iso")},
	}
	rep := &Report{CoreMembers: []string{"i386/ntoskrnl.exe"}}
	if err := emitWIMCDISO(context.Background(), m, tree, rep); err == nil {
		t.Fatal("missing reserved boot chain should fail the tail")
	}
}

func openISOFS(t *testing.T, path string) fs.FS {
	t.Helper()
	disk, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatal(err)
	}
	gofs, err := disk.GetFilesystem(0)
	if err != nil {
		t.Fatal(err)
	}
	fsys, ok := gofs.(fs.FS)
	if !ok {
		t.Fatal("iso filesystem is not fs.FS")
	}
	return fsys
}

// isoNames returns the sorted "/"-prefixed paths in an in-memory ISO image.
func isoNames(t *testing.T, image []byte) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "img.iso")
	if err := os.WriteFile(p, image, 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := openISOFS(t, p)
	var names []string
	if err := fs.WalkDir(fsys, ".", func(pp string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if pp != "." {
			names = append(names, "/"+pp)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

func gunzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("coreimg is not valid gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// assertNames checks that dir on the ISO contains each wanted name (presence only, extra
// entries are fine).
func assertNames(t *testing.T, fsys fs.FS, dir string, want ...string) {
	t.Helper()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	have := make(map[string]bool, len(entries))
	for _, e := range entries {
		have[e.Name()] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("ISO %s missing %q (has %v)", dir, w, keys(have))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
