// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inifile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/action"
)

// sifCRLF mirrors a slice of txtsetup.sif's [SetupData] with CRLF endings.
const sifCRLF = "[SourceDisksNames]\r\n1 = foo\r\n\r\n[SetupData]\r\n" +
	"SetupSourcePath = \"\\\"\r\n" +
	"OsLoadOptions = \"/fastdetect /noguiboot /nodebug\"\r\n" +
	"Architecture = i386\r\n\r\n" +
	"[Files]\r\nx = 1\r\n"

func TestSetValueReplacesInPlace(t *testing.T) {
	got := SetValue(sifCRLF, "SetupData", "osloadoptions", `"/fastdetect /minint"`)

	if !strings.Contains(got, "OsLoadOptions = \"/fastdetect /minint\"\r\n") {
		t.Errorf("osloadoptions not replaced in place:\n%q", got)
	}
	if strings.Contains(got, "noguiboot") {
		t.Error("old osloadoptions value survived")
	}
	// Everything else is untouched, and CRLF endings are preserved.
	if !strings.Contains(got, "Architecture = i386\r\n") || !strings.Contains(got, "[Files]\r\n") {
		t.Errorf("surrounding content disturbed:\n%q", got)
	}
	if strings.Contains(got, "\r\r") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\r") {
		t.Errorf("line endings corrupted:\n%q", got)
	}
	// The original key casing is kept; only the value changed.
	if n := strings.Count(got, "OsLoadOptions"); n != 1 {
		t.Errorf("expected one OsLoadOptions line, got %d", n)
	}
}

func TestSetValueAddsMissingKey(t *testing.T) {
	got := SetValue(sifCRLF, "SetupData", "loaderprompt", `"Starting pebuild..."`)

	if !strings.Contains(got, "loaderprompt = \"Starting pebuild...\"\r\n") {
		t.Errorf("loaderprompt not added:\n%q", got)
	}
	// It lands inside [SetupData] (before the [Files] header), after Architecture.
	setup := got[strings.Index(got, "[SetupData]"):strings.Index(got, "[Files]")]
	if !strings.Contains(setup, "loaderprompt") {
		t.Errorf("loaderprompt not placed within [SetupData]:\n%q", setup)
	}
}

func TestSetValueUnknownSectionAppends(t *testing.T) {
	got := SetValue("[A]\r\nx = 1\r\n", "B", "y", "2")
	if !strings.Contains(got, "[B]") || !strings.Contains(got, "y = 2") {
		t.Errorf("new section not appended:\n%q", got)
	}
}

func TestAddLineAndDedup(t *testing.T) {
	base := "[S]\r\na = 1\r\n"
	once := AddLine(base, "S", "b = 2")
	if !strings.Contains(once, "b = 2\r\n") {
		t.Errorf("line not added:\n%q", once)
	}
	twice := AddLine(once, "S", "b = 2")
	if strings.Count(twice, "b = 2") != 1 {
		t.Errorf("duplicate line added:\n%q", twice)
	}
}

func TestDelLineByPrefix(t *testing.T) {
	base := "[S]\r\nAddService = Foo, 1\r\nKeep = yes\r\n"
	got := DelLine(base, "S", "AddService = Foo,")
	if strings.Contains(got, "AddService") {
		t.Errorf("matching line not deleted:\n%q", got)
	}
	if !strings.Contains(got, "Keep = yes\r\n") {
		t.Errorf("non-matching line was removed:\n%q", got)
	}
}

func TestDelLineOnlyWithinSection(t *testing.T) {
	base := "[A]\r\nx = 1\r\n[B]\r\nx = 1\r\n"
	got := DelLine(base, "B", "x")
	// Only [B]'s x is removed.
	if strings.Count(got, "x = 1") != 1 {
		t.Errorf("DelLine crossed section boundary:\n%q", got)
	}
	if !strings.HasPrefix(got, "[A]\r\nx = 1\r\n") {
		t.Errorf("[A] disturbed:\n%q", got)
	}
}

func TestSetValueRepeatedSection(t *testing.T) {
	// Two [SetupData] blocks (setupapi merges repeated sections); the key lives in
	// the second. It must be replaced in place, not duplicated into the first.
	content := "[SetupData]\r\nA = 1\r\n[Other]\r\nz = 9\r\n[SetupData]\r\nOsLoadOptions = \"old\"\r\n"
	got := SetValue(content, "SetupData", "osloadoptions", `"new"`)
	if strings.Contains(got, `"old"`) {
		t.Errorf("value in the second block not replaced:\n%q", got)
	}
	if n := strings.Count(got, "OsLoadOptions"); n != 1 {
		t.Errorf("expected one OsLoadOptions line (no duplicate insert), got %d:\n%q", n, got)
	}
}

func TestAppendSectionPreservesTrailingBlanks(t *testing.T) {
	// A file ending in intentional trailing blank lines; a SetValue on an absent
	// section must not strip the padding.
	content := "[A]\r\nx = 1\r\n\r\n\r\n"
	got := SetValue(content, "B", "y", "2")
	if !strings.Contains(got, "[B]") || !strings.Contains(got, "y = 2") {
		t.Errorf("new section not appended:\n%q", got)
	}
	if !strings.HasSuffix(got, "\r\n\r\n\r\n") {
		t.Errorf("trailing blank lines not preserved:\n%q", got)
	}
}

func TestApplyOnTree(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "i386")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "txtsetup.sif"), []byte(sifCRLF), 0o644); err != nil {
		t.Fatal(err)
	}
	acts := []action.Action{
		{Kind: action.SetValue, TargetFile: "txtsetup.sif", TargetSection: "SetupData", LineKey: "osloadoptions", LineData: `"/fastdetect /minint"`},
		{Kind: action.SetValue, TargetFile: "txtsetup.sif", TargetSection: "SetupData", LineKey: "loaderprompt", LineData: `"Starting pebuild..."`},
	}
	placed := Index([]string{"i386/txtsetup.sif"})
	if err := Apply(acts, root, placed); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "txtsetup.sif"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "/fastdetect /minint") || strings.Contains(s, "noguiboot") {
		t.Errorf("osloadoptions edit not applied:\n%q", s)
	}
	if !strings.Contains(s, "loaderprompt = \"Starting pebuild...\"") {
		t.Errorf("loaderprompt not added:\n%q", s)
	}
}

// TestApplyUnplacedTargetSkipped: a target no copy placed is skipped, not fatal.
func TestApplyUnplacedTargetSkipped(t *testing.T) {
	acts := []action.Action{
		{Kind: action.SetValue, TargetFile: "nu2menu.xml", TargetSection: "S", LineKey: "x", LineData: "1"},
	}
	if err := Apply(acts, t.TempDir(), map[string]string{}); err != nil {
		t.Fatalf("unplaced target should be skipped, got error: %v", err)
	}
}

// TestApplyPlacedButUnreadableIsError: a target that was placed but is unreadable
// (or vanished) is a hard error — the base's must-land edits cannot be lost.
func TestApplyPlacedButUnreadableIsError(t *testing.T) {
	acts := []action.Action{
		{Kind: action.SetValue, TargetFile: "txtsetup.sif", TargetSection: "SetupData", LineKey: "x", LineData: "1"},
	}
	placed := Index([]string{"i386/txtsetup.sif"}) // claims placed, but the file is absent
	if err := Apply(acts, t.TempDir(), placed); err == nil {
		t.Fatal("Apply with a placed-but-missing target returned no error")
	}
}

// TestApplyResolvesSubdirTarget: an edit target placed in a subdirectory is found
// wherever the copy stage put it, not assumed at the system root.
func TestApplyResolvesSubdirTarget(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "i386", "system32")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nu2menu.xml"), []byte("[S]\r\na = 1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	acts := []action.Action{
		{Kind: action.SetValue, TargetFile: "nu2menu.xml", TargetSection: "S", LineKey: "a", LineData: "2"},
	}
	placed := Index([]string{"i386/system32/nu2menu.xml"})
	if err := Apply(acts, root, placed); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(sub, "nu2menu.xml"))
	if !strings.Contains(string(out), "a = 2") {
		t.Errorf("subdir target not edited in place:\n%q", out)
	}
}
