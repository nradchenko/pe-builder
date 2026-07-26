// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"os"
	"testing"
)

// testISO is the real XP source ISO symlinked under testdata/iso for local runs. Tests
// that need it skip when it is absent (e.g. CI).
const testISO = "../testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

func TestProbeISO(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	src, err := Open(testISO)
	if err != nil {
		t.Fatal(err)
	}
	id, err := src.Probe()
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	if id != want {
		t.Errorf("Probe() = %+v, want %+v", id, want)
	}
}

func TestProbeArchFolderMissing(t *testing.T) {
	root := writeTree(t, map[string]string{"readme.txt": "no arch folder"})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&source{backing: backing}).Probe(); err == nil {
		t.Error("Probe with no i386/amd64 folder returned no error")
	}
}

func TestProbeArchDetection(t *testing.T) {
	// A folder named amd64 (any case) with unreadable ntdll still fails at the
	// version step, but archFolder must have selected amd64 first.
	root := writeTree(t, map[string]string{"AMD64/placeholder": "x"})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// The folder is returned as the (canonical, lowercase) name we look up by; the
	// backing resolves it case-insensitively to the real AMD64 directory.
	folder, arch, err := (&source{backing: backing}).archFolder()
	if err != nil {
		t.Fatal(err)
	}
	if arch != "amd64" || folder != "amd64" {
		t.Errorf("archFolder = (%q, %q), want (amd64, amd64)", folder, arch)
	}
}

// TestDetectArch covers the architecture read a *built* PE gets. It cannot be probed
// — the ntdll archive Probe reads a version from is decompressed and moved during the
// build — so the folder layout is the only signal left, and it must give the same
// answer Probe's own folder scan does, including on a tree carrying both folders.
func TestDetectArch(t *testing.T) {
	cases := []struct {
		name   string
		tree   map[string]string
		want   string
		wantOK bool
	}{
		{"i386", map[string]string{"I386/placeholder": "x"}, "i386", true},
		{"amd64", map[string]string{"AMD64/placeholder": "x"}, "amd64", true},
		{"both folders is amd64", map[string]string{
			"I386/placeholder":  "x",
			"AMD64/placeholder": "x",
		}, "amd64", true},
		{"neither", map[string]string{"readme.txt": "x"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backing, err := openDir(writeTree(t, tc.tree))
			if err != nil {
				t.Fatal(err)
			}
			arch, ok := DetectArch(backing)
			if arch != tc.want || ok != tc.wantOK {
				t.Errorf("DetectArch = (%q, %v), want (%q, %v)", arch, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestProbeArchX64HasBothFolders(t *testing.T) {
	// x64 install media carries BOTH I386 (32-bit setup) and AMD64 (64-bit
	// binaries); the source must be identified as amd64, not i386.
	root := writeTree(t, map[string]string{
		"I386/placeholder":  "x",
		"AMD64/placeholder": "x",
	})
	backing, err := openDir(root)
	if err != nil {
		t.Fatal(err)
	}
	_, arch, err := (&source{backing: backing}).archFolder()
	if err != nil {
		t.Fatal(err)
	}
	if arch != "amd64" {
		t.Errorf("archFolder arch = %q, want amd64 (both I386 and AMD64 present)", arch)
	}
}
