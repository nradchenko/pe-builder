// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

// The listing hint is the one part of these helpers that varies by container, so it is
// the part a shared implementation can most easily get wrong: a hint that never fires
// leaves a wim-layout image looking almost empty with no explanation, and one that always
// fires puts a note about PE.wim on a scratch disk.
func TestPrintFileListHint(t *testing.T) {
	tree := fstest.MapFS{
		"PE.wim":   {Data: []byte("MSWIM")},
		"boot.img": {Data: []byte("core")},
	}

	tests := []struct {
		name     string
		pattern  string
		l        listing
		wantHint bool
	}{
		// Listing everything is one of the two cases an unexplained short answer misleads.
		{"everything, with a hint", "", listing{noun: "image", hint: "the payload is inside PE.wim"}, true},
		// Matching nothing is the other.
		{"no match, with a hint", "nosuch.dll", listing{noun: "image", hint: "the payload is inside PE.wim"}, true},
		// A pattern that matched is a complete answer on its own.
		{"a match, with a hint", "PE.wim", listing{noun: "image", hint: "the payload is inside PE.wim"}, false},
		// A container with nothing to add never gets a note — this is the disk case.
		{"everything, no hint", "", listing{noun: "disk"}, false},
		{"no match, no hint", "nosuch.dll", listing{noun: "disk"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := printFileList(&buf, tree, tt.pattern, false, tt.l); err != nil {
				t.Fatalf("printFileList: %v", err)
			}
			got := strings.Contains(buf.String(), "Note:")
			if got != tt.wantHint {
				t.Errorf("hint printed = %v, want %v; output:\n%s", got, tt.wantHint, buf.String())
			}
		})
	}
}

// The count is what a caller reads to know whether the pattern did anything, so it must
// follow the matching rather than the walk.
func TestPrintFileListCount(t *testing.T) {
	tree := fstest.MapFS{
		"I386/SYSTEM32/STARTNET.CMD": {Data: []byte("cmd")},
		"I386/NTDETECT.COM":          {Data: []byte("boot")},
	}
	tests := []struct {
		pattern string
		want    string
	}{
		{"", "2 file(s) matched."},
		{"*.cmd", "1 file(s) matched."},
		{"nosuch", "0 file(s) matched."},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if err := printFileList(&buf, tree, tt.pattern, false, listing{noun: "image"}); err != nil {
			t.Fatalf("printFileList(%q): %v", tt.pattern, err)
		}
		if !strings.Contains(buf.String(), tt.want) {
			t.Errorf("pattern %q: output does not contain %q:\n%s", tt.pattern, tt.want, buf.String())
		}
	}
}

// A malformed glob must be refused rather than reported as nothing matched, which would
// read as a confident answer to a question that was never asked.
func TestPrintFileListRejectsMalformedPattern(t *testing.T) {
	var buf bytes.Buffer
	err := printFileList(&buf, fstest.MapFS{"a.txt": {}}, "foo[", false, listing{noun: "image"})
	if err == nil {
		t.Fatal("want an error for a malformed pattern")
	}
	if buf.Len() != 0 {
		t.Errorf("a refused pattern wrote output:\n%s", buf.String())
	}
}

// A full path resolves whatever case it is typed in, which matters because the container
// this reads may be a FAT volume: its entry names come back in the guest's uppercase 8.3
// form, and fs.Stat compares them exactly where the volume folds case when opening. Naming
// the path a listing just printed has to work — and when the same base name exists
// elsewhere, the path is the disambiguation, so falling through to the base-name pass turns
// a precise request into "that is ambiguous, name the exact path".
func TestResolveNameFoldsCaseOnFullPaths(t *testing.T) {
	// Deliberately not an fstest.MapFS lookup away: MapFS is case-sensitive in Stat *and*
	// Open, which is exactly the FAT-like behaviour this has to cope with.
	tree := fstest.MapFS{
		"A/HELLO.TXT": {Data: []byte("a")},
		"B/HELLO.TXT": {Data: []byte("b")},
	}
	paths, err := walkFiles(tree)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"A/HELLO.TXT", "a/hello.txt", "A/hello.txt", `a\HELLO.TXT`} {
		got, err := resolveName(tree, paths, name, listing{noun: "disk"})
		if err != nil {
			t.Errorf("resolveName(%q): %v", name, err)
			continue
		}
		if got != "A/HELLO.TXT" {
			t.Errorf("resolveName(%q) = %q, want %q", name, got, "A/HELLO.TXT")
		}
	}

	// A bare name that two paths share is still ambiguous — the fix must not make the
	// base-name pass pick one.
	if got, err := resolveName(tree, paths, "hello.txt", listing{noun: "disk"}); err == nil {
		t.Errorf("resolveName(\"hello.txt\") = %q, want an ambiguity error", got)
	}
}

// The noun is what makes one shared implementation say "image" to one command and "disk"
// to another; a message that names the wrong container is worse than a generic one.
func TestResolveNameNoun(t *testing.T) {
	// Both copies are in subdirectories on purpose: an exact path is tried first, so a
	// file of the same name at the root would resolve before ambiguity is ever considered.
	tree := fstest.MapFS{"one/a.txt": {Data: []byte("a")}, "two/a.txt": {Data: []byte("b")}}
	paths, err := walkFiles(tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing", func(t *testing.T) {
		_, err := resolveName(tree, paths, "nosuch.dll", listing{noun: "disk"})
		if err == nil {
			t.Fatal("want an error")
		}
		if !strings.Contains(err.Error(), "in the disk") {
			t.Errorf("error %q does not name the container", err)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		_, err := resolveName(tree, paths, "a.txt", listing{noun: "disk"})
		if err == nil {
			t.Fatal("want an error for a name matching two files")
		}
		if !strings.Contains(err.Error(), "in this disk") {
			t.Errorf("error %q does not name the container", err)
		}
	})
}
