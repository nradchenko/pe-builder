// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package golden

import (
	"path/filepath"
	"testing"
)

func TestPath(t *testing.T) {
	got := Path("winxp/probe")
	want := filepath.Join("testdata", "winxp/probe.golden")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestRoundTripMatches(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate testdata/ writes to a throwaway dir

	const name, content = "sample", "hello\nworld\n"
	if err := writeGolden(name, []byte(content)); err != nil {
		t.Fatalf("writeGolden: %v", err)
	}

	diff, err := diffGolden(name, []byte(content))
	if err != nil {
		t.Fatalf("diffGolden: %v", err)
	}
	if diff != "" {
		t.Errorf("round-trip diff not empty:\n%s", diff)
	}
}

func TestDiffDetectsMismatch(t *testing.T) {
	t.Chdir(t.TempDir())

	const name = "sample"
	if err := writeGolden(name, []byte("expected")); err != nil {
		t.Fatalf("writeGolden: %v", err)
	}

	diff, err := diffGolden(name, []byte("actual"))
	if err != nil {
		t.Fatalf("diffGolden: %v", err)
	}
	if diff == "" {
		t.Error("expected a non-empty diff for differing content")
	}
}

func TestHierarchicalName(t *testing.T) {
	t.Chdir(t.TempDir())

	// A name with a slash (as produced by subtests via t.Name()) must create the
	// nested testdata/ subdirectory rather than failing.
	const name, content = "winxp/probe", "build 2600\n"
	if err := writeGolden(name, []byte(content)); err != nil {
		t.Fatalf("writeGolden(%q): %v", name, err)
	}
	diff, err := diffGolden(name, []byte(content))
	if err != nil {
		t.Fatalf("diffGolden(%q): %v", name, err)
	}
	if diff != "" {
		t.Errorf("round-trip diff not empty:\n%s", diff)
	}
}

func TestMissingGolden(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := diffGolden("absent", []byte("x")); err == nil {
		t.Error("expected an error reading a missing golden file")
	}
}

func TestAssertHappyPath(t *testing.T) {
	t.Chdir(t.TempDir())

	const name, content = "greeting", "hi there\n"
	if err := writeGolden(name, []byte(content)); err != nil {
		t.Fatalf("writeGolden: %v", err)
	}
	// Must not fail the test: the golden matches.
	Assert(t, name, content)
}
