// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/base"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/hive"
	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/source"
)

// minimalHiveINF is a valid, empty hive-population INF: setupapi accepts it (it
// carries the NT signature) and its empty [AddReg] adds nothing, so BaseHives can
// seed the SOFTWARE/DEFAULT/CLASSES hives in a hermetic test.
const minimalHiveINF = `[Version]
Signature = "$Windows NT$"

[AddReg]
`

const cacheISO = "../.cache/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

// syntheticMeta is minimal source metadata: a directory table and a couple of Load
// classes, enough for layout.Parse to place files and derive drivers.
const syntheticMeta = `[Version]
Signature = "$Windows NT$"

[WinntDirectories]
1 = "\"
2 = system32
4 = system32\drivers

[FileSystems.Load]
ntfs = ntfs.sys

[SCSI.Load]
atapi = atapi.sys
cabonly = cabonly.sys
`

// TestAssemblePipeline exercises the post-probe pipeline hermetically: it derives the
// base action set from synthetic metadata, materializes every base file loosely in
// the source, and asserts assemble places them all. It bypasses Probe (which gates on
// a real ntdll version resource) via the internal assemble entry point.
func TestAssemblePipeline(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}

	// setupreg.hiv is a real (empty) regf image; the three hive*.inf files seed the
	// remaining base hives. Together they let the hive stage run hermetically.
	sysHive, err := hive.WriteHive(hive.NewHive("$$$PROTO.HIV"))
	if err != nil {
		t.Fatal(err)
	}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
		"i386/setupreg.hiv": {Data: sysHive},
		"i386/hivesft.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivedef.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivecls.inf":  {Data: []byte(minimalHiveINF)},
	}

	// Populate every base copy file loosely, plus one of the two SCSI drivers so the
	// optional-skip path (cabonly.sys, absent) is exercised too.
	l, err := layout.Parse(backing, id)
	if err != nil {
		t.Fatal(err)
	}
	acts, err := base.Actions(l, id)
	if err != nil {
		t.Fatal(err)
	}
	// cabonly.sys stays absent (exercises the optional-skip path); txtsetup.sif keeps
	// its real metadata content rather than being clobbered with a stub, since the
	// hive/layout stages re-read it.
	present := map[string]bool{"cabonly.sys": false, "txtsetup.sif": true}
	for _, a := range acts {
		if _, decided := present[a.Src]; decided {
			continue
		}
		backing["i386/"+a.Src] = &fstest.MapFile{Data: []byte("stub:" + a.Src)}
	}

	out := t.TempDir()
	rep, err := assemble(context.Background(), backing, id, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected per-file errors: %v", rep.Errors)
	}

	tree := walkTree(t, out)
	summary := fmt.Sprintf("identity: %s\nwritten: %d\nskipped: %v\nhives: %v\n\n%s",
		rep.Identity, rep.Written, rep.Skipped, rep.Hives, tree)
	golden.Assert(t, "synthetic-tree", summary)
}

// TestBuildRealSource is the milestone end-to-end check: build the real XP SP3 source
// and diff the assembled tree (sorted path + size) against a committed golden listing.
// It skips when the source ISO is absent, keeping CI hermetic.
func TestBuildRealSource(t *testing.T) {
	if _, err := os.Stat(cacheISO); err != nil {
		t.Skip("real source ISO not present under .cache; skipping")
	}
	out := t.TempDir()
	m := &manifest.Manifest{
		Source: manifest.Source{Path: cacheISO, Type: "auto"},
		Build:  manifest.Build{WorkDir: out},
	}

	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("build reported per-file errors: %v", rep.Errors)
	}

	summary := fmt.Sprintf("identity: %s\nwritten: %d\nskipped: %s\n\n%s",
		rep.Identity, rep.Written, strings.Join(rep.Skipped, ","), walkTreeWithSizes(t, out))
	golden.Assert(t, "real-tree", summary)
}

// TestBuildISO is the end-to-end media check: build the real XP SP3 source with the
// iso target and confirm the produced image is bootable (El Torito boot record) and
// carries the boot chain — the loader at \I386, txtsetup.sif switched to /minint, and
// the SYSTEM hive — all readable back through the ISO.
func TestBuildISO(t *testing.T) {
	if _, err := os.Stat(cacheISO); err != nil {
		t.Skip("real source ISO not present under .cache; skipping")
	}
	out := filepath.Join(t.TempDir(), "rescue.iso")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: cacheISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetISO, Path: out, VolumeLabel: "PEBUILDER"},
		Build:  manifest.Build{WorkDir: filepath.Join(t.TempDir(), "tree")},
	}

	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath != out || rep.ISOSize == 0 {
		t.Fatalf("report ISO = %q (%d bytes)", rep.ISOPath, rep.ISOSize)
	}

	// The image has a readable El Torito no-emulation boot record.
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := boot.ExtractBootImage(f); err != nil {
		t.Errorf("boot record not readable: %v", err)
	}

	// Read the boot chain back through the ISO filesystem.
	src, err := source.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	b := src.Backing()
	if _, err := fs.ReadFile(b, "i386/setupldr.bin"); err != nil {
		t.Errorf("\\I386\\SETUPLDR.BIN not in ISO: %v", err)
	}
	if _, err := fs.ReadFile(b, "i386/system32/config/system"); err != nil {
		t.Errorf("SYSTEM hive not in ISO: %v", err)
	}
	txt, err := fs.ReadFile(b, "i386/txtsetup.sif")
	if err != nil {
		t.Fatalf("txtsetup.sif not in ISO: %v", err)
	}
	if !strings.Contains(string(txt), "/minint") {
		t.Error("txtsetup.sif in ISO does not carry the /minint switch")
	}
}

// TestBuildNone checks that the none target produces a complete PE tree — boot
// configuration applied (txtsetup.sif /minint, startnet.cmd) — but no media.
func TestBuildNone(t *testing.T) {
	if _, err := os.Stat(cacheISO); err != nil {
		t.Skip("real source ISO not present under .cache; skipping")
	}
	out := filepath.Join(t.TempDir(), "tree")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: cacheISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetNone},
		Build:  manifest.Build{WorkDir: out},
	}
	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath != "" {
		t.Errorf("none target produced media: %q", rep.ISOPath)
	}
	// The tree survives and carries its boot configuration.
	txt, err := os.ReadFile(filepath.Join(out, "i386", "txtsetup.sif"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(txt), "/minint") {
		t.Error("none tree txtsetup.sif not switched to /minint")
	}
	if _, err := os.Stat(filepath.Join(out, "i386", "system32", "startnet.cmd")); err != nil {
		t.Errorf("none tree missing startnet.cmd: %v", err)
	}
}

// TestBuildISOKeepWork checks that with keep_work the ISO is mastered from a clone,
// leaving the work tree intact.
func TestBuildISOKeepWork(t *testing.T) {
	if _, err := os.Stat(cacheISO); err != nil {
		t.Skip("real source ISO not present under .cache; skipping")
	}
	tree := filepath.Join(t.TempDir(), "tree")
	out := filepath.Join(t.TempDir(), "rescue.iso")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: cacheISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetISO, Path: out, VolumeLabel: "PEBUILDER"},
		Build:  manifest.Build{WorkDir: tree, KeepWork: true},
	}
	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath == "" || rep.OutDir != tree {
		t.Fatalf("keep_work: ISOPath=%q OutDir=%q (want tree kept)", rep.ISOPath, rep.OutDir)
	}
	// Both the image and the (non-consumed) tree exist.
	if _, err := os.Stat(out); err != nil {
		t.Errorf("iso not produced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "i386", "setupldr.bin")); err != nil {
		t.Errorf("work tree not kept: %v", err)
	}
}

func TestBuildBadSource(t *testing.T) {
	m := &manifest.Manifest{Source: manifest.Source{Path: filepath.Join(t.TempDir(), "nope")}}
	if _, err := Build(context.Background(), m); err == nil {
		t.Fatal("Build with a nonexistent source returned no error")
	}
}

// TestAssembleCancelled checks that a cancelled context aborts the build fatally
// rather than being demoted to a non-fatal report error (which would let a cancelled
// build look successful).
func TestAssembleCancelled(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := assemble(ctx, backing, id, t.TempDir())
	if err == nil {
		t.Fatalf("assemble with a cancelled context returned no error (rep=%+v)", rep)
	}
}

// walkTree returns the sorted output-relative paths of files under root.
func walkTree(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n") + "\n"
}

// walkTreeWithSizes returns sorted "path size" lines for every file under root — the
// deterministic (decompressed) shape used for the golden tree diff.
func walkTreeWithSizes(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		lines = append(lines, fmt.Sprintf("%s %d", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
