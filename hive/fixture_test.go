// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"io/fs"
	"testing"

	"github.com/nradchenko/pe-builder/internal/repo"
	"github.com/nradchenko/pe-builder/source"
)

// sourceBacking opens the real source ISO as a source backing. The Microsoft hive
// material (setupreg.hiv, hive*.inf) is never committed, so the tests that need it read
// it from that ISO and skip when it is absent (e.g. CI).
func sourceBacking(t *testing.T) source.Backing {
	t.Helper()
	src, err := source.Open(repo.SourceISO(t))
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
