// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package layout

import (
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/source"
)

// parseFiles builds a Layout from just a [SourceDisksFiles] block, which is all these
// tests are about. The directory table comes along because Parse needs one.
func parseFiles(t *testing.T, files string) *Layout {
	t.Helper()
	sif := "[WinntDirectories]\n1 = \\\n2 = \\system32\n" + files
	l, err := Parse(fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(sif)},
		"i386/layout.inf":   {Data: []byte("[WinntDirectories]\n1 = \\\n")},
	}, source.Identity{Arch: "i386", Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// A source may keep a file under one name and install it under another, and the second is
// the one everything loads by. Field 10 of a [SourceDisksFiles] entry declares it. Reading
// only the name on the left made whole files unreachable: a localized medium keeps the
// service-pack resource satellites language-coded, so a build placed sprt0419.dll where
// netshell would look for xpsp2res.dll, and every search for the canonical name reported
// the medium as never having carried it.

func TestDestNameIsRead(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
sprt0419.dll = 100,,2929664,,,,,2,0,0,xpsp2res.dll,1,2
ntoskrnl.exe = 100,,2190848,,,,,2,0,0,,1,2
`)
	if got := l.Files["sprt0419.dll"].DestName; got != "xpsp2res.dll" {
		t.Errorf("DestName = %q, want %q", got, "xpsp2res.dll")
	}
	// The common case declares no rename, and must not invent one.
	if got := l.Files["ntoskrnl.exe"].DestName; got != "" {
		t.Errorf("a file with no rename got DestName %q, want empty", got)
	}
}

// A rename that restates the file's own name is not a rename. Carrying it would make every
// such entry look like a mapping and put a needless name through the placement path.
func TestDestNameSameAsSourceIsDropped(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
aaclient.dll = 100,,,,,,,2,0,0,aaclient.dll,1,2
`)
	if got := l.Files["aaclient.dll"].DestName; got != "" {
		t.Errorf("DestName = %q, want empty when it repeats the source name", got)
	}
}

func TestStoredAsFindsTheSourceName(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
sprt0419.dll = 100,,2929664,,,,,2,0,0,xpsp2res.dll,1,2
`)
	got, ok := l.StoredAs("XPSP2RES.DLL") // asked for the way a consumer names it
	if !ok || got != "sprt0419.dll" {
		t.Errorf("StoredAs = (%q, %v), want (\"sprt0419.dll\", true)", got, ok)
	}
	if _, ok := l.StoredAs("notdeclared.dll"); ok {
		t.Errorf("StoredAs answered for a name nothing declares")
	}
}

// The case that makes guessing unacceptable: Server 2003 has 23 files declaring
// ws03res.dll, one per MUI language, each placed in its own directory. Picking one would
// pick by map order — a different answer on each run.
func TestStoredAsRefusesAnAmbiguousName(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
spra0816.dll = 100,,,,,,,21,0,0,ws03res.dll,1,2
spra0c0a.dll = 100,,,,,,,22,0,0,ws03res.dll,1,2
`)
	if got, ok := l.StoredAs("ws03res.dll"); ok {
		t.Errorf("StoredAs = (%q, true) for a name two files claim; want a refusal", got)
	}
}

// An amd64 source stores the WOW64 32-bit copies under w-prefixed names and declares each
// installed as the ordinary name, so wkernel32.dll claims kernel32.dll. The mapping is real
// and StoredAs reports it; what must never happen is a caller reaching for it to satisfy a
// missing 64-bit file, which would stage the wrong architecture's binary silently. That
// guard lives at the call site (a declared name is never aliased), and this pins the shape
// the guard exists for.
func TestStoredAsReportsTheWow64Alias(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
kernel32.dll  = 100,,,,,,,2,0,0,,1,2
wkernel32.dll = 100,,,,,,,11,0,0,kernel32.dll,1,2
`)
	if got, ok := l.StoredAs("kernel32.dll"); !ok || got != "wkernel32.dll" {
		t.Errorf("StoredAs = (%q, %v), want (\"wkernel32.dll\", true)", got, ok)
	}
	// The 64-bit file is declared in its own right, which is what the caller checks before
	// ever consulting the alias.
	if _, declared := l.Resolve("kernel32.dll"); !declared {
		t.Errorf("kernel32.dll must be declared in its own right")
	}
}

// A file listed again in a later block with no directory keeps its placement — the rule
// that already existed — and must also keep, or gain, its declared destination.
func TestDestNameSurvivesARepeatedEntry(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
sprt0419.dll = 100,,2929664,,,,,2,0,0,xpsp2res.dll,1,2
sprt0419.dll = 100
`)
	e := l.Files["sprt0419.dll"]
	if e.TargetDir != "2" {
		t.Errorf("TargetDir = %q, want %q — a bare repeat must not blank the placement", e.TargetDir, "2")
	}
	if e.DestName != "xpsp2res.dll" {
		t.Errorf("DestName = %q, want %q — a bare repeat must not blank the destination", e.DestName, "xpsp2res.dll")
	}
}

// The other shape of repeat, and the one that actually overwrites: a later block that
// carries a directory but no destination name. The two fields are independent — such an
// entry says nothing about the name — so keeping the directory while dropping the name
// would quietly put the file back under the name it is stored under.
func TestDestNameSurvivesARepeatedEntryCarryingADirectory(t *testing.T) {
	l := parseFiles(t, `
[SourceDisksFiles]
sprt0419.dll = 100,,2929664,,,,,2,0,0,xpsp2res.dll,1,2
sprt0419.dll = 100,,2929664,,,,,2,0,0,,1,2
`)
	if got := l.Files["sprt0419.dll"].DestName; got != "xpsp2res.dll" {
		t.Errorf("DestName = %q, want %q — a repeat with a directory must not blank it", got, "xpsp2res.dll")
	}
}
