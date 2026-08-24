// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package repo resolves paths inside this repository for tests that read files the
// repository ships: a plugin's .inf, the manifest field catalogue, the real Windows
// source ISO symlinked under testdata/.
//
// It exists so that no test counts "../" to reach the repository root. Such a path is a
// hidden dependency on how deep its own package sits, and the failure it produces is
// silent: a test that skips when its file is missing cannot tell a stale path from a
// file that is legitimately absent, so moving its package one directory deeper switches
// the coverage off while the package still reports ok.
//
// Resolving the root is therefore fatal when it fails, and kept separate from whether
// the file it names exists — which is the caller's business, and the only thing allowed
// to skip.
//
// Every function here takes a testing.TB, so this package is for tests only: importing it
// from production code would link testing into pebuild. Nothing does, and nothing should.
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// module is the path this repository's go.mod declares. Matching on it makes the walk
// self-validating: a go.mod sitting somewhere above the checkout would otherwise be
// accepted as the root, and every path built from it would be silently wrong.
const module = "github.com/nradchenko/pe-builder"

// root is resolved once, as the test binary starts — before any test body runs, and so
// before any test can change the working directory. A failure travels in rootErr to the
// first caller, which has a testing.TB to report it with.
var root, rootErr = findRoot()

// findRoot walks up from the working directory — go test sets it to the directory of
// the package under test — to the directory holding this repository's go.mod.
func findRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	for dir := start; ; {
		if declaresModule(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod declaring module %s above %s", module, start)
		}
		dir = parent
	}
}

// declaresModule reports whether path is a go.mod for this repository's module. It
// accepts every form the go.mod grammar allows for the directive — a quoted path, a
// trailing comment, and the parenthesized block — because failing to recognize this
// repository's own go.mod would send the walk past the real root, and every test that
// resolves a path would then die on a root it could not find.
func declaresModule(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inBlock := false
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		// A go.mod declares one module, so the first path reached settles it either way.
		switch {
		case inBlock:
			return f[0] != ")" && isModulePath(f[0])
		case f[0] != "module":
			continue
		case len(f) == 2 && f[1] == "(":
			inBlock = true
		case len(f) == 2:
			return isModulePath(f[1])
		}
	}
	return false
}

// isModulePath reports whether a module path as go.mod writes it — quoted or bare — is
// this repository's.
func isModulePath(field string) bool {
	if unquoted, err := strconv.Unquote(field); err == nil {
		field = unquoted
	}
	return field == module
}

// Root returns the repository root, failing the test if it could not be resolved rather
// than returning a value a caller would quietly build wrong paths from.
func Root(t testing.TB) string {
	t.Helper()
	if rootErr != nil {
		t.Fatalf("repo: locate repository root: %v", rootErr)
	}
	return root
}

// Path returns the absolute path of elem... under the repository root. The elements are
// passed separately rather than as one slash-joined string so that nothing in a caller
// reads as a relative walk.
func Path(t testing.TB, elem ...string) string {
	t.Helper()
	return filepath.Join(append([]string{Root(t)}, elem...)...)
}

// sourceISO names the Windows source ISO the media-reading tests run against, relative
// to the repository root. The ISO itself is never committed — testdata/iso holds a
// symlink into a local, gitignored cache — so a checkout without it is normal, and CI
// is one.
var sourceISO = []string{"testdata", "iso", "ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"}

// SourceISO returns the path to that ISO, skipping the test when it is not present. Its
// absence is the one condition that skips: the path is resolved first, and fatally, so a
// test can never skip merely because it was looking in the wrong place.
func SourceISO(t testing.TB) string {
	t.Helper()
	p := SourceISOPath(t)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("real source ISO not present at %s; skipping", p)
	}
	return p
}

// SourceISOPath returns where that ISO would be, without checking that it is there. It
// serves a caller that accepts media from elsewhere too and so owns its own fallback and
// its own skip; taking the path from here is what keeps the ISO's name in one place.
func SourceISOPath(t testing.TB) string {
	t.Helper()
	return Path(t, sourceISO...)
}
