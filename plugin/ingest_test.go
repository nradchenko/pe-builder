// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip writes a .zip containing files (path -> contents) and returns its path.
func makeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddZipLooseFiles(t *testing.T) {
	// Loose files at the archive root → the plugin folder is the archive base name.
	arc := makeZip(t, map[string]string{
		"sample.inf": "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"S\"\n",
		"readme.txt": "hello",
	})
	dest := t.TempDir()

	name, err := Add(arc, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != "archive" {
		t.Errorf("name = %q, want the archive base name %q", name, "archive")
	}
	assertFileContent(t, filepath.Join(dest, "archive", "sample.inf"), "[Version]")
	assertFileContent(t, filepath.Join(dest, "archive", "readme.txt"), "hello")

	// The result is discoverable by the search path.
	names, _ := DirSource{Root: dest}.List()
	if len(names) != 1 || names[0] != "archive" {
		t.Errorf("List() = %v, want [archive]", names)
	}
}

func TestAddZipFolder(t *testing.T) {
	// A single top-level folder → that folder is the plugin name, paths preserved.
	arc := makeZip(t, map[string]string{
		"myplugin/myplugin.inf": "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"M\"\n",
		"myplugin/files/x.dat":  "data",
	})
	dest := t.TempDir()

	name, err := Add(arc, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != "myplugin" {
		t.Errorf("name = %q, want myplugin", name)
	}
	assertFileContent(t, filepath.Join(dest, "myplugin", "myplugin.inf"), "PEBuilder")
	assertFileContent(t, filepath.Join(dest, "myplugin", "files", "x.dat"), "data")
}

func TestAddCabWithoutInfRejected(t *testing.T) {
	// single.cab holds a single hello.txt and no .inf, so it is not a plugin: the cab
	// path is exercised (format detect + extract), then Add rejects it rather than
	// installing a directory the search path could never discover.
	dest := t.TempDir()
	_, err := Add("testdata/single.cab", dest, false)
	if err == nil {
		t.Fatal("Add installed a cab with no .inf; want a rejection")
	}
	if !strings.Contains(err.Error(), ".inf") {
		t.Errorf("error %q should explain the missing .inf", err)
	}
	// Nothing (not even a temp dir) was left behind.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dest not clean after a rejected add: %v", entries)
	}
}

func TestAddRejectsMultiTopDir(t *testing.T) {
	// Two top-level directories → wrapped under the archive name, so no .inf sits at the
	// plugin root → rejected, not a silent bad install.
	arc := makeZip(t, map[string]string{
		"foo/foo.inf": "[Version]\n[PEBuilder]\n",
		"bar/bar.dat": "data",
	})
	if _, err := Add(arc, t.TempDir(), false); err == nil {
		t.Error("Add accepted a multi-top-dir archive with no .inf at the plugin root")
	}
}

func TestAddRejectsNoInf(t *testing.T) {
	arc := makeZip(t, map[string]string{"readme.txt": "no inf here"})
	if _, err := Add(arc, t.TempDir(), false); err == nil {
		t.Error("Add accepted an archive with no .inf")
	}
}

func TestAddRejectsPathTraversal(t *testing.T) {
	arc := makeZip(t, map[string]string{"../evil.txt": "pwned"})
	dest := t.TempDir()
	if _, err := Add(arc, dest, false); err == nil {
		t.Error("Add accepted a zip with a ..-escaping member")
	}
	// Nothing was written outside dest.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); err == nil {
		t.Error("path-traversal member escaped the destination")
	}
}

func TestAddConflict(t *testing.T) {
	arc := makeZip(t, map[string]string{"a/a.inf": "x"})
	dest := t.TempDir()

	if _, err := Add(arc, dest, false); err != nil {
		t.Fatal(err)
	}
	// A second install without --force is refused.
	if _, err := Add(arc, dest, false); err == nil {
		t.Error("Add over an existing plugin without force returned no error")
	}
	// With force it overwrites and drops files no longer in the archive.
	arc2 := makeZip(t, map[string]string{"a/b.inf": "y"})
	if _, err := Add(arc2, dest, true); err != nil {
		t.Fatalf("forced overwrite failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a", "a.inf")); err == nil {
		t.Error("force overwrite left the stale a.inf behind")
	}
	assertFileContent(t, filepath.Join(dest, "a", "b.inf"), "y")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("%s = %q, want it to contain %q", path, b, want)
	}
}
