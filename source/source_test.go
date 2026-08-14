// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityString(t *testing.T) {
	id := Identity{Build: 2600, Arch: "i386"}
	got := id.String()
	want := "build 2600 (i386)"
	if got != want {
		t.Errorf("Identity.String() = %q, want %q", got, want)
	}
}

func TestIdentityRoots(t *testing.T) {
	tests := []struct {
		name         string
		arch         string
		system, boot string
	}{
		// An x86 source keeps both areas in the one folder — the case that let a
		// single constant stand in for both for as long as it did.
		{"i386", "i386", "i386", "i386"},
		// An x64 source splits them: the OS payload moves to AMD64, the real-mode
		// boot chain stays in I386.
		{"amd64", "amd64", "amd64", "i386"},
		// A zero Identity resolves to the x86 layout rather than an empty folder.
		{"unset", "", "i386", "i386"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Identity{Arch: tt.arch}.Roots()
			if r.System != tt.system || r.Boot != tt.boot {
				t.Errorf("Identity{Arch: %q}.Roots() = %+v, want {System: %q, Boot: %q}",
					tt.arch, r, tt.system, tt.boot)
			}
			if got := r.For(AreaSystem); got != tt.system {
				t.Errorf("For(AreaSystem) = %q, want %q", got, tt.system)
			}
			if got := r.For(AreaBoot); got != tt.boot {
				t.Errorf("For(AreaBoot) = %q, want %q", got, tt.boot)
			}
		})
	}
}

// The boot area is i386 on every architecture: its files are real-mode, so they are
// never architecture-specific. This is the whole point of separating the two areas,
// so it is asserted on its own rather than only as a column above.
func TestBootAreaIsArchIndependent(t *testing.T) {
	for _, arch := range []string{"i386", "amd64", ""} {
		if got := (Identity{Arch: arch}).Roots().For(AreaBoot); got != "i386" {
			t.Errorf("boot root for arch %q = %q, want %q", arch, got, "i386")
		}
	}
}

func TestAreaString(t *testing.T) {
	if got := AreaSystem.String(); got != "system" {
		t.Errorf("AreaSystem.String() = %q, want %q", got, "system")
	}
	if got := AreaBoot.String(); got != "boot" {
		t.Errorf("AreaBoot.String() = %q, want %q", got, "boot")
	}
	// An area String does not know renders as its number rather than masquerading
	// as a real one, so a debug line naming it cannot mislead.
	if got := Area(42).String(); got != "area(42)" {
		t.Errorf("Area(42).String() = %q, want %q", got, "area(42)")
	}
}

// An area For does not know resolves to the system folder — the documented safe
// direction, since every area but the boot chain is part of the OS payload. Pinned
// here so the fallback stays a deliberate choice rather than drifting.
func TestForUnknownAreaResolvesToSystem(t *testing.T) {
	r := Identity{Arch: "amd64"}.Roots()
	if got := r.For(Area(42)); got != r.System {
		t.Errorf("For(unknown area) = %q, want the system root %q", got, r.System)
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
