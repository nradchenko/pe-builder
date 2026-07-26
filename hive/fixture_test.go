// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"io/fs"
	"os"
	"testing"

	"github.com/nradchenko/pe-builder/source"
)

// testISO is the real XP SP3 source ISO symlinked under testdata/iso for local runs.
// The Microsoft hive material (setupreg.hiv, hive*.inf) is never committed, so
// tests that need it read it from this ISO and skip when it is absent (e.g. CI).
const testISO = "../testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

// sourceBacking opens the test ISO as a source backing, skipping the test when
// the ISO is not present.
func sourceBacking(t *testing.T) source.Backing {
	t.Helper()
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	src, err := source.Open(testISO)
	if err != nil {
		t.Fatalf("open source ISO: %v", err)
	}
	return src.Backing()
}

// sourceFile reads a file from the source backing by its i386-relative name.
func sourceFile(t *testing.T, b source.Backing, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(b, "i386/"+name)
	if err != nil {
		t.Fatalf("read %s from source: %v", name, err)
	}
	return data
}

// setupregHive reads and parses i386/setupreg.hiv from the cache ISO into a tree.
func setupregHive(t *testing.T) *Hive {
	t.Helper()
	b := sourceBacking(t)
	h, err := ReadHive(sourceFile(t, b, "setupreg.hiv"))
	if err != nil {
		t.Fatalf("ReadHive(setupreg.hiv): %v", err)
	}
	return h
}
