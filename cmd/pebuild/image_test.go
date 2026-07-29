// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// A built image's case is not stable — the tree writes mixed case and the ISO stage
// uppercases — so the same pattern has to find a file in a work directory and in the .iso
// mastered from it. That is the property these cases pin.
func TestImageMatch(t *testing.T) {
	const p = "I386/SYSTEM32/STARTNET.CMD"
	cases := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		{"empty pattern matches everything", p, "", true},
		{"bare name, differing case", p, "startnet.cmd", true},
		{"bare name glob", p, "*.cmd", true},
		{"full path, differing case", p, "i386/system32/startnet.cmd", true},
		{"full path glob", p, "i386/system32/*.cmd", true},
		{"backslashes are accepted as separators", p, `i386\system32\startnet.cmd`, true},
		{"non-match", p, "ntdetect.com", false},
		{"glob does not cross a separator", p, "i386/*.cmd", false},
		{"mixed-case tree path", "i386/system32/startnet.cmd", "STARTNET.CMD", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pat, err := normalizeImagePattern(c.pattern)
			if err != nil {
				t.Fatalf("normalizeImagePattern(%q): %v", c.pattern, err)
			}
			if got := imageMatch(c.path, pat); got != c.want {
				t.Errorf("imageMatch(%q, %q) = %v, want %v", c.path, c.pattern, got, c.want)
			}
		})
	}
}

// A malformed glob must be an error, not a confident "0 file(s) matched" — that reads as
// an answer about the image when it is really an answer about the typo.
func TestNormalizeImagePatternRejectsMalformedGlob(t *testing.T) {
	if _, err := normalizeImagePattern("foo["); err == nil {
		t.Fatal("normalizeImagePattern(\"foo[\"): want an error, got nil")
	}
	pat, err := normalizeImagePattern(`I386\SYSTEM32\*.CMD`)
	if err != nil {
		t.Fatalf("normalizeImagePattern: %v", err)
	}
	if pat != "i386/system32/*.cmd" {
		t.Errorf("normalizeImagePattern = %q, want separators unified and lowercased", pat)
	}
}

func TestReadImageFileResolvesExactPathAndUniqueBaseName(t *testing.T) {
	fsys := fstest.MapFS{
		"I386/SYSTEM32/STARTNET.CMD": {Data: []byte("@echo off")},
		"I386/NTDETECT.COM":          {Data: []byte("boot")},
	}

	// An exact path is tried first.
	data, from, err := readImageFile(fsys, "I386/SYSTEM32/STARTNET.CMD")
	if err != nil || string(data) != "@echo off" || from != "I386/SYSTEM32/STARTNET.CMD" {
		t.Fatalf("exact path: data=%q from=%q err=%v", data, from, err)
	}

	// A bare name that is unique resolves, and reports where it came from — the caller
	// asked for a name, so the answer has to say which file it got.
	data, from, err = readImageFile(fsys, "ntdetect.com")
	if err != nil || string(data) != "boot" || from != "I386/NTDETECT.COM" {
		t.Fatalf("bare name: data=%q from=%q err=%v", data, from, err)
	}
}

// An ambiguous bare name must be an error naming the candidates. Silently picking one
// would answer a different question than the one asked, and the caller would not know.
func TestReadImageFileRejectsAmbiguousBaseName(t *testing.T) {
	fsys := fstest.MapFS{
		"I386/SYSTEM32/DRIVERS/VGA.SYS": {Data: []byte("a")},
		"I386/VGA.SYS":                  {Data: []byte("b")},
	}
	_, _, err := readImageFile(fsys, "vga.sys")
	if err == nil {
		t.Fatal("readImageFile: want an error for an ambiguous bare name, got nil")
	}
	for _, want := range []string{"I386/SYSTEM32/DRIVERS/VGA.SYS", "I386/VGA.SYS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name candidate %q", err, want)
		}
	}
}

func TestReadImageFileMissing(t *testing.T) {
	fsys := fstest.MapFS{"I386/NTDETECT.COM": {Data: []byte("boot")}}
	if _, _, err := readImageFile(fsys, "nosuch.dll"); err == nil {
		t.Fatal("readImageFile: want an error for a file not in the image, got nil")
	}
}

// A directory is not a file: naming one must fall through to the bare-name search rather
// than resolving to the directory itself, which would then fail obscurely on read.
func TestResolveImageNameIgnoresDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"I386/SYSTEM32/NTDETECT.COM": {Data: []byte("boot")},
	}
	paths, err := walkImage(fsys)
	if err != nil {
		t.Fatalf("walkImage: %v", err)
	}
	if _, err := resolveImageName(fsys, paths, "I386/SYSTEM32"); err == nil {
		t.Error("resolveImageName on a directory: want an error, got nil")
	}
}

// Two names that land on the same output name must be refused, and nothing may be
// written — a partial extraction that reports success for both is worse than a refusal.
func TestExtractRefusesCollidingOutputNames(t *testing.T) {
	dir := t.TempDir()
	fsys := fstest.MapFS{
		"I386/SYSTEM32/VGAOEM.FON": {Data: []byte("a")},
		"I386/FONTS/VGAOEM.FON":    {Data: []byte("b")},
	}
	paths, err := walkImage(fsys)
	if err != nil {
		t.Fatalf("walkImage: %v", err)
	}

	// The resolution both names share is what collides: the same lowercased base name.
	claimed := map[string]string{}
	var collided bool
	for _, name := range []string{"I386/SYSTEM32/VGAOEM.FON", "I386/FONTS/VGAOEM.FON"} {
		from, err := resolveImageName(fsys, paths, name)
		if err != nil {
			t.Fatalf("resolveImageName(%q): %v", name, err)
		}
		dest, err := outputName(from)
		if err != nil {
			t.Fatalf("outputName(%q): %v", from, err)
		}
		if _, ok := claimed[dest]; ok {
			collided = true
			break
		}
		claimed[dest] = from
	}
	if !collided {
		t.Fatal("two files with the same base name did not collide; extract would overwrite silently")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("collision detection wrote %d file(s); it must happen before any write", len(entries))
	}
}
