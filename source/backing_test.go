// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// writeTree writes a set of relative path -> contents files under a fresh temp
// directory and returns its path.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The backing is deliberately not a strictly-conformant io/fs.FS: it tolerates the
// mixed case and separators that plugin .inf paths use, which fstest.TestFS (rightly)
// rejects. So conformance is checked here by exercising the access paths the pipeline
// actually uses — Open, fs.ReadFile, fs.ReadDir — against valid, canonical names.
func TestDirBackingAccess(t *testing.T) {
	root := writeTree(t, map[string]string{
		"I386/NTDLL.DLL": "pe",
		"I386/ATAPI.SY_": "cab",
		"AUTORUN.INF":    "auto",
	})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := fs.ReadFile(backing, "I386/NTDLL.DLL"); err != nil || string(data) != "pe" {
		t.Errorf("ReadFile(I386/NTDLL.DLL) = %q, %v", data, err)
	}
	entries, err := fs.ReadDir(backing, "I386")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadDir(I386) returned %d entries, want 2", len(entries))
	}
}

func TestNormalizationLookup(t *testing.T) {
	root := writeTree(t, map[string]string{"I386/NTDLL.DLL": "pe"})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"I386/NTDLL.DLL",   // exact
		"i386/ntdll.dll",   // lowercase
		"i386\\ntdll.dll",  // backslash + lowercase
		"/I386/NTDLL.DLL",  // leading slash
		"I386/./NTDLL.DLL", // dot component
	} {
		data, err := fs.ReadFile(backing, name)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", name, err)
			continue
		}
		if string(data) != "pe" {
			t.Errorf("ReadFile(%q) = %q, want %q", name, data, "pe")
		}
	}
}

func TestNormalizationMissing(t *testing.T) {
	root := writeTree(t, map[string]string{"I386/NTDLL.DLL": "pe"})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backing.Open("I386/MISSING.DLL"); err == nil {
		t.Error("Open of a genuinely absent file returned no error")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open of a missing file: err = %v, want fs.ErrNotExist", err)
	}
}

// errFS is a backing whose every Open fails with a fixed error, standing in for a
// read/corruption failure from a real backing.
type errFS struct{ err error }

func (e errFS) Open(string) (fs.File, error) { return nil, e.err }

func TestOpenSurfacesRealError(t *testing.T) {
	// A genuine I/O error must be returned as-is, not masked as fs.ErrNotExist —
	// even when its message contains not-found-sounding words (which the old
	// substring classifier would have misread).
	sentinel := errors.New("iso read failed: sector not found")
	backing := normFS{fsys: errFS{err: sentinel}}
	_, err := backing.Open("I386/NTDLL.DLL")
	if !errors.Is(err, sentinel) {
		t.Errorf("Open error = %v, want the original sentinel", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("real read error was masked as fs.ErrNotExist")
	}
}

func TestOpenDirRejectsFile(t *testing.T) {
	root := writeTree(t, map[string]string{"a.txt": "x"})
	if _, err := openDir(filepath.Join(root, "a.txt")); err == nil {
		t.Error("openDir of a regular file returned no error")
	}
}
