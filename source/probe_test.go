// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"os"
	"testing"
)

// cacheISO is the real XP source ISO symlinked under .cache for local runs. Tests
// that need it skip when it is absent (e.g. CI).
const cacheISO = "../.cache/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

func TestProbeISO(t *testing.T) {
	if _, err := os.Stat(cacheISO); err != nil {
		t.Skip("real source ISO not present under .cache; skipping")
	}
	src, err := Open(cacheISO)
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
