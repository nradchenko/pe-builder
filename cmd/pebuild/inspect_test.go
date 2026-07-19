// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/hive"
	"github.com/nradchenko/pe-builder/inf"
)

// TestOutputNameStaysInsideDirectory covers the extraction path that takes its output
// name from data rather than from the user: a cabinet member name is a raw string out
// of the archive's file table, and the cabinet format allows a path there. Every such
// name has to reduce to a bare filename, or an extraction writes outside the directory
// the user asked for.
func TestOutputNameStaysInsideDirectory(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "ntoskrnl.exe", "ntoskrnl.exe"},
		{"lowercased", "NTOSKRNL.EXE", "ntoskrnl.exe"},
		{"source path", "i386/system32/kbda1.dll", "kbda1.dll"},
		{"cabinet path", `subdir\nested\foo.dll`, "foo.dll"},
		{"slash traversal", "../../../etc/passwd", "passwd"},
		{"backslash traversal", `..\..\..\windows\evil.dll`, "evil.dll"},
		{"absolute", "/etc/passwd", "passwd"},
		{"mixed separators", `../a/..\b/evil.sys`, "evil.sys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := outputName(tc.in)
			if err != nil {
				t.Fatalf("outputName(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("outputName(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("outputName(%q) = %q still carries a separator", tc.in, got)
			}
		})
	}
}

// TestOutputNameRejectsNonFiles covers the names that reduce to no filename at all.
// They must be refused rather than joined onto the output directory, where they would
// name the directory itself or its parent.
func TestOutputNameRejectsNonFiles(t *testing.T) {
	for _, in := range []string{"", ".", "..", "/", `\`, "../..", "foo/.."} {
		if got, err := outputName(in); err == nil {
			t.Errorf("outputName(%q) = %q, want an error", in, got)
		}
	}
}

// TestDumpValueAtHiveRoot covers a value sitting directly under the hive root, whose
// path has no separator to split a parent off. The root is that value's parent, so it
// is addressable like any other.
func TestDumpValueAtHiveRoot(t *testing.T) {
	h := hive.NewHive("Root")
	h.Root.SetValue(hive.Value{Name: "RootValue", Type: hive.RegSZ, Data: hive.EncodeSZ("here")})
	sub := h.Root.EnsurePath(`Nested\Key`)
	sub.SetValue(hive.Value{Name: "Deep", Type: hive.RegSZ, Data: hive.EncodeSZ("there")})

	var buf bytes.Buffer
	if err := dumpHive(&buf, h, "RootValue", 0, false); err != nil {
		t.Fatalf("dump root value: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, `RootValue = "here"`) {
		t.Errorf("root value not dumped, got:\n%s", got)
	}

	buf.Reset()
	if err := dumpHive(&buf, h, `Nested\Key\Deep`, 0, false); err != nil {
		t.Fatalf("dump nested value: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, `Deep = "there"`) {
		t.Errorf("nested value not dumped, got:\n%s", got)
	}

	buf.Reset()
	if err := dumpHive(&buf, h, "NoSuchThing", 0, false); err == nil {
		t.Error("dumping an absent name succeeded, want an error")
	}
}

// TestPrintINFSectionFindsBuildOnlySection covers a section that exists only in its
// .BuildNo form. Presence cannot be decided from the base name alone, or the very
// sections --build exists to select report as absent.
func TestPrintINFSectionFindsBuildOnlySection(t *testing.T) {
	f, err := inf.Parse([]byte("[Files.2600]\nfoo = bar\n\n[Empty]\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var buf bytes.Buffer
	if err := printINFSection(&buf, f, "Files", 2600, false); err != nil {
		t.Fatalf("build-suffixed section reported absent: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "foo = bar") {
		t.Errorf("build-suffixed lines missing, got:\n%s", got)
	}

	// A section with no lines is still a section, and still not the same as absent.
	buf.Reset()
	if err := printINFSection(&buf, f, "Empty", 2600, false); err != nil {
		t.Errorf("empty section reported absent: %v", err)
	}
	if err := printINFSection(&buf, f, "Missing", 2600, false); err == nil {
		t.Error("absent section reported present")
	}

	// Without the matching build number the suffixed section stays out of reach,
	// which is what makes it conditional in the first place.
	buf.Reset()
	if err := printINFSection(&buf, f, "Files", 3790, false); err == nil {
		t.Error("section for another build number reported present")
	}
}

// TestSplitRegPath covers the hive-selecting half of a registry path, including the
// HKLM prefix these paths are habitually written with.
func TestSplitRegPath(t *testing.T) {
	cases := []struct {
		in       string
		wantHive string
		wantKey  string
	}{
		{`SYSTEM\ControlSet001\Services`, "system", `ControlSet001\Services`},
		{`HKLM\SOFTWARE\Microsoft`, "software", "Microsoft"},
		{`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft`, "software", "Microsoft"},
		{`hklm\system`, "system", ""},
		{"SOFTWARE", "software", ""},
		{`Microsoft\Windows`, "", `Microsoft\Windows`},
		{"", "", ""},
		{`SYSTEM/ControlSet001`, "system", "ControlSet001"},
	}
	for _, tc := range cases {
		gotHive, gotKey := splitRegPath(tc.in)
		if gotHive != tc.wantHive || gotKey != tc.wantKey {
			t.Errorf("splitRegPath(%q) = (%q, %q), want (%q, %q)",
				tc.in, gotHive, gotKey, tc.wantHive, tc.wantKey)
		}
	}
}
