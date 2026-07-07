// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityString(t *testing.T) {
	id := Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	got := id.String()
	want := "winxp build 2600 (i386)"
	if got != want {
		t.Errorf("Identity.String() = %q, want %q", got, want)
	}
}

func TestOpenMissing(t *testing.T) {
	src, err := Open(t.TempDir() + "/does-not-exist")
	if err == nil {
		t.Error("Open of a missing path returned no error")
	}
	if src != nil {
		t.Errorf("Open returned a non-nil source on error: %v", src)
	}
}

func TestRequireImageSource(t *testing.T) {
	if err := RequireImageSource(t.TempDir()); err == nil {
		t.Error("RequireImageSource accepted a directory")
	}
	p := filepath.Join(t.TempDir(), "img.iso")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequireImageSource(p); err != nil {
		t.Errorf("RequireImageSource rejected a file: %v", err)
	}
}

func TestOpenImageFile(t *testing.T) {
	// A directory source has no boot image and must be rejected.
	if _, err := OpenImageFile(t.TempDir()); err == nil {
		t.Error("OpenImageFile on a directory returned no error")
	}
	// A regular file opens.
	p := filepath.Join(t.TempDir(), "img.iso")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenImageFile(p)
	if err != nil {
		t.Fatalf("OpenImageFile on a file: %v", err)
	}
	f.Close()
	// A nonexistent path errors.
	if _, err := OpenImageFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("OpenImageFile on a missing path returned no error")
	}
}
