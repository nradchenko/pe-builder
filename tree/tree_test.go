// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// testRoots is the x86 folder pair the fixtures below are laid out under, where the
// system and boot areas are the same i386 folder.
func testRoots() source.Roots {
	return source.Identity{Arch: "i386"}.Roots()
}

// TestAssembleAmd64Roots assembles against a source whose two areas are *different*
// folders. Every other test here uses an x86 source, where the system and boot areas
// are both "i386" — so any confusion between them resolves to the same string and
// passes. This is the only test that can tell them apart, and so the only one that
// would catch the system area being read from or written to the boot folder.
func TestAssembleAmd64Roots(t *testing.T) {
	out := t.TempDir()
	roots := source.Identity{Arch: "amd64"}.Roots()
	if roots.System == roots.Boot {
		t.Fatalf("amd64 roots must differ to make this test meaningful: %+v", roots)
	}

	// Each area holds a decoy of the other's file, so resolving the wrong area finds
	// something and would pass silently rather than erroring on a missing file.
	src := fstest.MapFS{
		"amd64/loose.txt": {Data: []byte("amd64 payload\n")},
		"i386/loose.txt":  {Data: []byte("boot-area decoy\n")},
		"i386/ldr.bin":    {Data: []byte("real-mode loader\n")},
		"amd64/ldr.bin":   {Data: []byte("system-area decoy\n")},
	}
	acts := []action.Action{
		{Kind: action.CopyFile, Src: "loose.txt", DirID: "2"},
		// The real-mode boot chain: read from and written to the boot area, pinned at
		// the source root (directory ID 1) like the loader the CD boot sector chains to.
		{Kind: action.CopyFile, Src: "ldr.bin", DirID: "1", Area: source.AreaBoot},
		// Directories carry an area too. These create nothing a copy would create, so
		// only makeDir's own area resolution can place them — a misplaced directory
		// writes no file and so would leave the assertions on Written untouched.
		{Kind: action.Mkdir, DirID: "p", Path: "payloaddir", AlwaysCreate: true},
		{Kind: action.Mkdir, DirID: "b", Path: "bootdir", AlwaysCreate: true, Area: source.AreaBoot},
	}

	res, err := Assemble(context.Background(), acts, testLayout(), src, roots, nil, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []string{"amd64/system32/loose.txt", "i386/ldr.bin"}
	if diff := cmp.Diff(want, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	// The payload resolves through the system area, not the boot area's decoy.
	assertFile(t, out, "amd64/system32/loose.txt", []byte("amd64 payload\n"))
	// The boot chain resolves through the boot area, not the system area's decoy —
	// the two areas are read and written independently and never cross.
	assertFile(t, out, "i386/ldr.bin", []byte("real-mode loader\n"))
	// Each directory lands under its own area's folder, and under no other.
	assertDir(t, filepath.Join(out, "amd64", "payloaddir"))
	assertDir(t, filepath.Join(out, "i386", "bootdir"))
	for _, stray := range []string{"i386/payloaddir", "amd64/bootdir"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(stray))); !os.IsNotExist(err) {
			t.Errorf("directory created in the wrong area: %s exists", stray)
		}
	}
}

// TestCabinetsAreSystemAreaOnly checks that the driver cabinets answer only for the
// OS payload. They are part of that payload — they sit in the system folder — so
// serving a boot-chain file from one would take one area's bytes for the other's
// file, the crossing the area exists to prevent. The same name resolves from the
// cabinet in the system area and is simply not found in the boot area.
func TestCabinetsAreSystemAreaOnly(t *testing.T) {
	cab, err := os.ReadFile(filepath.Join("testdata", "lzx-single.cab"))
	if err != nil {
		t.Fatal(err)
	}
	roots := source.Identity{Arch: "amd64"}.Roots()
	// A driver cabinet in the system area, holding hello.txt. Nothing is loose, so a
	// copy of that name can only be answered by the cabinet.
	src := fstest.MapFS{"amd64/driver.cab": {Data: cab}}
	hello, err := os.ReadFile(filepath.Join("testdata", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	sys := []action.Action{{Kind: action.CopyFile, Src: "hello.txt", DirID: "2"}}
	if _, err := Assemble(context.Background(), sys, testLayout(), src, roots, nil, out, nil); err != nil {
		t.Fatalf("system-area copy should resolve from the cabinet: %v", err)
	}
	assertFile(t, out, "amd64/system32/hello.txt", hello)

	// The boot-area copy is paired with a system-area copy of the SAME name, so the
	// member really is extracted and sitting in the cabinet resolver — the only state
	// in which a cross-area answer is possible. Excluding boot-area names from the
	// wanted set is not enough to prevent it; the read itself has to refuse.
	both := []action.Action{
		{Kind: action.CopyFile, Src: "hello.txt", DirID: "2"},
		{Kind: action.CopyFile, Src: "hello.txt", DirID: "2", Area: source.AreaBoot},
	}
	res, err := Assemble(context.Background(), both, testLayout(), src, roots, nil, t.TempDir(), nil)
	if err == nil {
		t.Error("boot-area copy was answered from a system-area cabinet; want not found")
	}
	// The system-area copy of the same name still resolves — refusing the boot-area
	// read must not break the payload path it shares a name with.
	if res == nil || len(res.Written) != 1 || res.Written[0] != "amd64/system32/hello.txt" {
		t.Errorf("system-area copy did not resolve alongside the refused boot-area one: %+v", res)
	}
}

// TestOutputPathFollowsArch pins the output-path rule directly: the same action
// resolves under whichever folder the source's system area names.
func TestOutputPathFollowsArch(t *testing.T) {
	a := action.Action{Kind: action.CopyFile, Src: "loose.txt", DirID: "2"}
	for _, tt := range []struct{ arch, want string }{
		{"i386", "i386/system32/loose.txt"},
		{"amd64", "amd64/system32/loose.txt"},
	} {
		roots := source.Identity{Arch: tt.arch}.Roots()
		if got := OutputPath(a, testLayout(), roots); got != tt.want {
			t.Errorf("OutputPath(arch=%s) = %q, want %q", tt.arch, got, tt.want)
		}
	}
}

// testLayout is a small placement model: system32 and its drivers subdirectory.
func testLayout() *layout.Layout {
	return &layout.Layout{
		Dirs: map[string]string{
			"1": "",
			"2": "system32",
			"4": "system32/drivers",
		},
		Files: map[string]layout.FileEntry{
			"loose.txt":   {Name: "loose.txt", TargetDir: "2"},
			"packed.dll":  {Name: "packed.dll", TargetDir: "4"},
			"keepcab.dll": {Name: "keepcab.dll", TargetDir: "2"},
		},
	}
}

// testSource holds a loose file and two copies of the single-file LZX cabinet under
// compressed (.??_) names, mirroring how a real I386 folder stores files.
func testSource(t *testing.T) fstest.MapFS {
	t.Helper()
	cab, err := os.ReadFile(filepath.Join("testdata", "lzx-single.cab"))
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"i386/loose.txt":   {Data: []byte("loose contents\n")},
		"i386/packed.dl_":  {Data: cab},
		"i386/keepcab.dl_": {Data: cab},
	}
}

func TestAssemble(t *testing.T) {
	src := testSource(t)
	out := t.TempDir()

	acts := []action.Action{
		{Kind: action.CopyFile, Src: "loose.txt", DirID: "2"},
		{Kind: action.CopyFile, Src: "packed.dll"}, // dir resolved via layout → drivers
		{Kind: action.CopyFile, Src: "keepcab.dll", NoDecompress: true},
		{Kind: action.CopyFile, Src: "missing.dll", Optional: true}, // absent, optional → skipped
	}

	res, err := Assemble(context.Background(), acts, testLayout(), src, testRoots(), nil, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	wantWritten := []string{
		"i386/system32/drivers/packed.dll",
		"i386/system32/keepcab.dl_",
		"i386/system32/loose.txt",
	}
	if diff := cmp.Diff(wantWritten, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"missing.dll"}, res.Skipped); diff != "" {
		t.Errorf("Skipped mismatch (-want +got):\n%s", diff)
	}

	// Loose file copied verbatim.
	assertFile(t, out, "i386/system32/loose.txt", []byte("loose contents\n"))
	// Compressed file decompressed to the known plaintext.
	hello, err := os.ReadFile(filepath.Join("testdata", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, out, "i386/system32/drivers/packed.dll", hello)
	// NoDecompress: the compressed archive is copied as-is under its .??_ name.
	cab, _ := os.ReadFile(filepath.Join("testdata", "lzx-single.cab"))
	assertFile(t, out, "i386/system32/keepcab.dl_", cab)
}

func TestAssembleOrigin(t *testing.T) {
	src := testSource(t)
	out := t.TempDir()

	// tool.exe is shipped by the plugin (Origin set, present in the origin FS);
	// loose.txt is a Windows-source file the plugin merely names (Origin empty).
	origins := map[string]fs.FS{
		"plug": fstest.MapFS{"tool.exe": {Data: []byte("plugin tool\n")}},
	}
	acts := []action.Action{
		{Kind: action.CopyFile, Src: "tool.exe", DirID: "2", Origin: "plug"},
		{Kind: action.CopyFile, Src: "loose.txt", DirID: "2"},
	}

	res, err := Assemble(context.Background(), acts, testLayout(), src, testRoots(), origins, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if diff := cmp.Diff([]string{"i386/system32/loose.txt", "i386/system32/tool.exe"}, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	assertFile(t, out, "i386/system32/tool.exe", []byte("plugin tool\n"))
	assertFile(t, out, "i386/system32/loose.txt", []byte("loose contents\n"))
}

func TestAssembleOriginMissingIsError(t *testing.T) {
	// A file the plugin promised to ship (Origin set) but is absent from its FS is an
	// error, not a silent fall-through to a same-named Windows-source file.
	src := testSource(t)
	origins := map[string]fs.FS{"plug": fstest.MapFS{}}
	acts := []action.Action{
		{Kind: action.CopyFile, Src: "loose.txt", DirID: "2", Origin: "plug"},
	}
	if _, err := Assemble(context.Background(), acts, testLayout(), src, testRoots(), origins, t.TempDir(), nil); err == nil {
		t.Fatal("Assemble placed an un-shipped Origin file from the source; want an error")
	}
}

func TestAssemblePluginDir(t *testing.T) {
	// A copy targeting a directory the plugin defines itself (Path carried on the copy)
	// lands there, not in the system32 fallback. FromRoot places it at the output root.
	out := t.TempDir()
	origins := map[string]fs.FS{
		"plug": fstest.MapFS{
			"tool.exe": {Data: []byte("t")},
			"read.me":  {Data: []byte("r")},
		},
	}
	acts := []action.Action{
		{Kind: action.CopyFile, Src: "tool.exe", Origin: "plug", DirID: "q", Path: `programs\mytool`},
		{Kind: action.CopyFile, Src: "read.me", Origin: "plug", DirID: "r", Path: "Docs", FromRoot: true},
	}
	res, err := Assemble(context.Background(), acts, testLayout(), testSource(t), testRoots(), origins, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []string{"Docs/read.me", "i386/programs/mytool/tool.exe"}
	if diff := cmp.Diff(want, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	assertFile(t, out, "i386/programs/mytool/tool.exe", []byte("t"))
	assertFile(t, out, "Docs/read.me", []byte("r"))
}

func TestAssembleRequiredMissing(t *testing.T) {
	src := testSource(t)
	acts := []action.Action{{Kind: action.CopyFile, Src: "nope.dll"}}
	_, err := Assemble(context.Background(), acts, testLayout(), src, testRoots(), nil, t.TempDir(), nil)
	if err == nil {
		t.Fatal("Assemble with a missing required file returned no error")
	}
}

// TestAssembleSourcePath checks a [SourcePaths] copy (SrcPath + literal Dest): it reads
// the exact nested source path — not a basename that would resolve a different file — and
// writes it verbatim to the given output path, decompressing a .??_ source form.
func TestAssembleSourcePath(t *testing.T) {
	out := t.TempDir()
	cab, err := os.ReadFile(filepath.Join("testdata", "lzx-single.cab"))
	if err != nil {
		t.Fatal(err)
	}
	// A same-basename decoy at the system root: SrcPath must reach the nested file, not it.
	src := fstest.MapFS{
		"i386/comctl32.dll":                    {Data: []byte("v5 system32 decoy\n")},
		"i386/asms/6000/controls/comctl32.dll": {Data: []byte("v6 assembly\n")},
		"i386/asms/6000/controls/controls.ma_": {Data: cab}, // compressed source form (.man → .ma_)
	}
	acts := []action.Action{
		{Kind: action.CopyFile, SrcPath: "asms/6000/controls/comctl32.dll", Dest: "i386/WinSxS/asm/comctl32.dll"},
		{Kind: action.CopyFile, SrcPath: "asms/6000/controls/controls.man", Dest: "i386/WinSxS/Manifests/asm.manifest"},
	}
	res, err := Assemble(context.Background(), acts, testLayout(), src, testRoots(), nil, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []string{"i386/WinSxS/Manifests/asm.manifest", "i386/WinSxS/asm/comctl32.dll"}
	if diff := cmp.Diff(want, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	// The exact nested file was copied verbatim (not the system32 decoy).
	assertFile(t, out, "i386/WinSxS/asm/comctl32.dll", []byte("v6 assembly\n"))
	// The .??_ source form was decompressed to the known plaintext.
	hello, err := os.ReadFile(filepath.Join("testdata", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, out, "i386/WinSxS/Manifests/asm.manifest", hello)
}

// TestAssembleSourcePathMissing checks that an absent [SourcePaths] source errors by its
// exact path, not the empty basename.
func TestAssembleSourcePathMissing(t *testing.T) {
	acts := []action.Action{
		{Kind: action.CopyFile, SrcPath: "asms/6000/controls/comctl32.dll", Dest: "i386/WinSxS/asm/comctl32.dll"},
	}
	_, err := Assemble(context.Background(), acts, testLayout(), fstest.MapFS{}, testRoots(), nil, t.TempDir(), nil)
	if err == nil {
		t.Fatal("missing [SourcePaths] source returned no error")
	}
	if !strings.Contains(err.Error(), "asms/6000/controls/comctl32.dll") {
		t.Errorf("error %q should name the missing source path", err)
	}
}

// TestWriteStaysInsideOutputTree checks that a destination climbing out of the output tree
// is refused rather than written. Nothing upstream rejects "..": normalizeTreePath only
// swaps separators and trims the ends, so a plugin's [SourcePaths] destination, a manifest
// inject target, or a [WinntDirectories] entry can all name a path outside the work
// directory, which the build would then write with its own privileges. Both the copy and
// the mkdir site are covered, since each assembles its path separately.
func TestWriteStaysInsideOutputTree(t *testing.T) {
	src := fstest.MapFS{"i386/comctl32.dll": {Data: []byte("payload\n")}}
	for _, tt := range []struct {
		name string
		act  action.Action
	}{
		{"copy with Dest climbing out", action.Action{
			Kind: action.CopyFile, SrcPath: "comctl32.dll", Dest: "../escaped.dll"}},
		{"copy with Dest climbing out via a subdirectory", action.Action{
			Kind: action.CopyFile, SrcPath: "comctl32.dll", Dest: "i386/../../escaped.dll"}},
		{"mkdir climbing out", action.Action{
			Kind: action.Mkdir, Path: "../escaped-dir", FromRoot: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := t.TempDir()
			// A sibling of the output tree: if the guard fails, this is where the write
			// lands, so assert on the filesystem rather than only on the error.
			outside := filepath.Dir(out)
			_, err := Assemble(context.Background(), []action.Action{tt.act}, testLayout(),
				src, testRoots(), nil, out, nil)
			if err == nil {
				t.Fatal("a destination outside the output tree was accepted")
			}
			for _, leaked := range []string{"escaped.dll", "escaped-dir"} {
				if _, statErr := os.Stat(filepath.Join(outside, leaked)); statErr == nil {
					t.Errorf("wrote %q outside the output tree", leaked)
				}
			}
		})
	}
}

// TestTargetName pins the output-name rule: lowercase by default (the tree convention),
// upper-cased when the copy carries the uppercase attribute (bit 8) — the original plugin
// format's flag for a file a real-mode reader looks up by an uppercase name on CD-boot media
// the ISO stage does not re-case. A rename target is subject to the same casing.
func TestTargetName(t *testing.T) {
	for _, tt := range []struct {
		name string
		a    action.Action
		in   string
		want string
	}{
		{"default lowercases", action.Action{}, "CDLOAD.BIN", "cdload.bin"},
		{"already lower stays", action.Action{}, "grldr", "grldr"},
		{"uppercase attribute", action.Action{Uppercase: true}, "cdload.bin", "CDLOAD.BIN"},
		{"uppercase single-letter name", action.Action{Uppercase: true}, "a", "A"},
		{"rename is lowercased", action.Action{Rename: "Loader.Bin"}, "ignored", "loader.bin"},
		{"rename is uppercased", action.Action{Rename: "loader.bin", Uppercase: true}, "ignored", "LOADER.BIN"},
	} {
		if got := targetName(tt.a, tt.in); got != tt.want {
			t.Errorf("%s: targetName(%+v, %q) = %q, want %q", tt.name, tt.a, tt.in, got, tt.want)
		}
	}
}

// TestAssembleUppercase drives the uppercase attribute end to end: a copy with it set is
// placed under the upper-cased name, a copy without it under the lowercase default — the
// original format's attribute-8 behavior, for a file a real-mode reader looks up by an
// uppercase name on CD, while a default copy (like grldr, which grub reads case-blind) stays
// lowercase.
func TestAssembleUppercase(t *testing.T) {
	out := t.TempDir()
	origins := map[string]fs.FS{
		"boot": fstest.MapFS{
			"cdload.bin": {Data: []byte("loader")},
			"cdload.cfg": {Data: []byte("config")},
			"grldr":      {Data: []byte("grub")},
		},
	}
	acts := []action.Action{
		{Kind: action.CopyFile, Src: "cdload.bin", Origin: "boot", Path: `media\boot`, FromRoot: true, Uppercase: true},
		{Kind: action.CopyFile, Src: "cdload.cfg", Origin: "boot", Path: `media\boot`, FromRoot: true, Uppercase: true},
		{Kind: action.CopyFile, Src: "grldr", Origin: "boot", Path: "media", FromRoot: true}, // default lowercase
	}
	res, err := Assemble(context.Background(), acts, testLayout(), testSource(t), testRoots(), origins, out, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []string{"media/boot/CDLOAD.BIN", "media/boot/CDLOAD.CFG", "media/grldr"}
	if diff := cmp.Diff(want, res.Written); diff != "" {
		t.Errorf("Written mismatch (-want +got):\n%s", diff)
	}
	assertFile(t, out, "media/boot/CDLOAD.BIN", []byte("loader"))
	assertFile(t, out, "media/boot/CDLOAD.CFG", []byte("config"))
	assertFile(t, out, "media/grldr", []byte("grub"))
}

func TestMkdir(t *testing.T) {
	out := t.TempDir()
	acts := []action.Action{
		{Kind: action.Mkdir, DirID: "x", Path: "programs", AlwaysCreate: true},
		{Kind: action.Mkdir, DirID: "y", Path: "docs", FromRoot: true},
		{Kind: action.Mkdir, DirID: "z", Path: `system32\config`}, // backslash nested path
	}
	if _, err := Assemble(context.Background(), acts, testLayout(), testSource(t), testRoots(), nil, out, nil); err != nil {
		t.Fatal(err)
	}
	assertDir(t, filepath.Join(out, "i386", "programs")) // under the system root
	assertDir(t, filepath.Join(out, "docs"))             // from the output root
	// A backslashed [WinntDirectories] path becomes nested directories, not one
	// directory whose name contains a literal backslash.
	assertDir(t, filepath.Join(out, "i386", "system32", "config"))
}

func TestOverlay(t *testing.T) {
	custom := t.TempDir()
	if err := os.MkdirAll(filepath.Join(custom, "programs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "programs", "tool.exe"), []byte("TOOL"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	// A pre-existing file the overlay must overwrite (last-writer-wins).
	if err := os.WriteFile(filepath.Join(out, "readme"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "readme"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := Overlay(context.Background(), out, custom)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"programs/tool.exe", "readme"}, written); diff != "" {
		t.Errorf("Overlay written mismatch (-want +got):\n%s", diff)
	}
	assertFile(t, out, "programs/tool.exe", []byte("TOOL"))
	assertFile(t, out, "readme", []byte("new"))
}

func assertFile(t *testing.T, root, rel string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if !cmp.Equal(want, got) {
		t.Errorf("%s: contents differ (len want %d, got %d)", rel, len(want), len(got))
	}
}

func assertDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}
