// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package golden is a tiny, format-agnostic golden-file test helper: it compares a
// test's actual output against a committed testdata/<name>.golden fixture, or
// regenerates the fixture when the test binary is run with -update.
//
// It deliberately knows nothing about what it compares (file trees, hive dumps,
// parsed INF, ...); callers render their output to a string or bytes and name the
// fixture. Golden files live under testdata/ relative to the calling test's package.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata/ instead of comparing")

const (
	goldenDir = "testdata"
	goldenExt = ".golden"
)

// Path returns the golden file path for name: testdata/<name>.golden.
func Path(name string) string {
	return filepath.Join(goldenDir, name+goldenExt)
}

// Assert compares got against the golden fixture named name. When the test binary is
// run with -update, it rewrites the fixture instead.
func Assert(t testing.TB, name, got string) {
	t.Helper()
	AssertBytes(t, name, []byte(got))
}

// AssertBytes is the []byte form of Assert.
func AssertBytes(t testing.TB, name string, got []byte) {
	t.Helper()

	if *update {
		if err := writeGolden(name, got); err != nil {
			t.Fatalf("golden: update %s: %v", Path(name), err)
		}
		return
	}

	diff, err := diffGolden(name, got)
	if err != nil {
		t.Fatalf("golden: read %s: %v (run `go test -update` to create it)", Path(name), err)
	}
	if diff != "" {
		t.Errorf("golden mismatch for %s (-want +got):\n%s", Path(name), diff)
	}
}

// writeGolden writes data to the fixture named name, creating its parent directory
// if needed (name may be hierarchical, e.g. a subtest's "group/case").
func writeGolden(name string, data []byte) error {
	path := Path(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// diffGolden reads the fixture named name and returns its go-cmp diff against got
// (empty when they match).
func diffGolden(name string, got []byte) (string, error) {
	want, err := os.ReadFile(Path(name))
	if err != nil {
		return "", err
	}
	return cmp.Diff(string(want), string(got)), nil
}
