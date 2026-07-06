// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
)

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

	res, err := Assemble(context.Background(), acts, testLayout(), src, out)
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

func TestAssembleRequiredMissing(t *testing.T) {
	src := testSource(t)
	acts := []action.Action{{Kind: action.CopyFile, Src: "nope.dll"}}
	_, err := Assemble(context.Background(), acts, testLayout(), src, t.TempDir())
	if err == nil {
		t.Fatal("Assemble with a missing required file returned no error")
	}
}

func TestMkdir(t *testing.T) {
	out := t.TempDir()
	acts := []action.Action{
		{Kind: action.Mkdir, DirID: "x", Path: "programs", AlwaysCreate: true},
		{Kind: action.Mkdir, DirID: "y", Path: "docs", FromRoot: true},
		{Kind: action.Mkdir, DirID: "z", Path: `system32\config`}, // backslash nested path
	}
	if _, err := Assemble(context.Background(), acts, testLayout(), testSource(t), out); err != nil {
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
