// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package layout

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/source"
)

// testISO is the real XP source ISO symlinked under testdata/iso for local runs. Tests
// needing it skip when it is absent (e.g. CI), keeping the suite hermetic.
const testISO = "../testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

// syntheticTxtsetup is a trimmed txtsetup.sif exercising the directory table, files
// with varying empty fields (so the target-directory index must be read positionally),
// a bare presence-check entry, and a couple of *.Load classes.
const syntheticTxtsetup = `[Version]
Signature = "$Windows NT$"

[WinntDirectories]
1  = "\"
2  = system32
4  = system32\drivers
20 = inf

[SourceDisksFiles]
cmd.exe      = 100,,396288,,,,,2,0,0,,1,2
ntoskrnl.exe = 100,,2190848,,,,,,3,3,,1,2
atapi.sys    = 100,,96512,,,,4_,4,0,0,,1,4
ntfs.sys     = 100,,574976,,,,_5,4,0,0,,1,4

[SourceDisksFiles]
cmd.exe = 1

[FileSystems.Load]
ntfs = ntfs.sys
fat  = fastfat.sys
ntfs = ntfs.sys

[SCSI.Load]
atapi = atapi.sys,4
`

// syntheticLayout gives cmd.exe an explicit directory that agrees with txtsetup and
// contributes a file txtsetup omits, to prove both files are merged.
const syntheticLayout = `[Version]
Signature = "$Windows NT$"

[SourceDisksFiles]
cmd.exe   = 100,,396288,,,,,2,0,0,,1,2
gdi32.dll = 100,,285184,,,,,2,0,0,,1,2
`

func synthSource(t *testing.T) source.Backing {
	t.Helper()
	return fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticTxtsetup)},
		"i386/layout.inf":   {Data: []byte(syntheticLayout)},
	}
}

func TestParseDirs(t *testing.T) {
	l, err := Parse(synthSource(t), source.Identity{Arch: "i386", Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"1":  "", // system root (the lone backslash)
		"2":  "system32",
		"4":  "system32/drivers",
		"20": "inf",
	}
	if diff := cmp.Diff(want, l.Dirs); diff != "" {
		t.Errorf("Dirs mismatch (-want +got):\n%s", diff)
	}
}

func TestResolve(t *testing.T) {
	l, err := Parse(synthSource(t), source.Identity{Arch: "i386", Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		wantDir string
		wantOK  bool
	}{
		{"cmd.exe", "2", true},
		{"CMD.EXE", "2", true},      // case-insensitive
		{"ntoskrnl.exe", "2", true}, // empty metadata dir → system32 default
		{"atapi.sys", "4", true},    // drivers
		{"ntfs.sys", "4", true},     // drivers
		{"gdi32.dll", "2", true},    // contributed by layout.inf only
		{"absent.dll", "", false},
	}
	for _, c := range cases {
		e, ok := l.Resolve(c.name)
		if ok != c.wantOK {
			t.Errorf("Resolve(%q) ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && e.TargetDir != c.wantDir {
			t.Errorf("Resolve(%q).TargetDir = %q, want %q", c.name, e.TargetDir, c.wantDir)
		}
	}
}

func TestBareEntryDoesNotClobber(t *testing.T) {
	// The second [SourceDisksFiles] block lists "cmd.exe = 1" (a bare presence
	// check with no target directory); it must not blank cmd.exe's placement.
	l, err := Parse(synthSource(t), source.Identity{Arch: "i386", Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := l.Resolve("cmd.exe"); e.TargetDir != "2" {
		t.Errorf("cmd.exe TargetDir = %q, want 2 (bare entry clobbered it)", e.TargetDir)
	}
}

func TestLoadAndBootDrivers(t *testing.T) {
	l, err := Parse(synthSource(t), source.Identity{Arch: "i386", Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"ntfs.sys", "fastfat.sys"}, l.Load["FileSystems"]); diff != "" {
		t.Errorf("FileSystems.Load mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"atapi.sys"}, l.Load["SCSI"]); diff != "" {
		t.Errorf("SCSI.Load mismatch (-want +got):\n%s", diff)
	}
	// BootDrivers unions the classes in class order, deduped. SCSI is consulted
	// before FileSystems, so atapi leads the union.
	if diff := cmp.Diff([]string{"atapi.sys", "ntfs.sys", "fastfat.sys"}, l.BootDrivers()); diff != "" {
		t.Errorf("BootDrivers mismatch (-want +got):\n%s", diff)
	}
}

func TestCompressedName(t *testing.T) {
	cases := map[string]string{
		"ntdll.dll":    "ntdll.dl_",
		"atapi.sys":    "atapi.sy_",
		"cmd.exe":      "cmd.ex_",
		"c_1252.nls":   "c_1252.nl_",
		"ntdetect.com": "ntdetect.co_",
		"":             "",
	}
	for in, want := range cases {
		if got := CompressedName(in); got != want {
			t.Errorf("CompressedName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseRealSource parses the real XP SP3 metadata and asserts a stable summary
// (directory count, a spread of resolutions, driver-class sizes) via a golden file.
func TestParseRealSource(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	src, err := source.Open(testISO)
	if err != nil {
		t.Fatal(err)
	}
	id, err := src.Probe()
	if err != nil {
		t.Fatal(err)
	}
	l, err := Parse(src.Backing(), id)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "dirs: %d\n", len(l.Dirs))
	fmt.Fprintf(&b, "files: %d\n", len(l.Files))
	fmt.Fprintf(&b, "\n# directory table\n")
	for _, id := range sortedKeysNumeric(l.Dirs) {
		fmt.Fprintf(&b, "%s = %s\n", id, l.Dirs[id])
	}
	fmt.Fprintf(&b, "\n# spot resolutions\n")
	for _, name := range []string{
		"ntoskrnl.exe", "hal.dll", "ntdll.dll", "kernel32.dll", "cmd.exe",
		"win32k.sys", "csrss.exe", "smss.exe", "ntfs.sys", "atapi.sys",
	} {
		if e, ok := l.Resolve(name); ok {
			fmt.Fprintf(&b, "%s -> dir %s (%s)\n", name, e.TargetDir, l.Dirs[e.TargetDir])
		} else {
			fmt.Fprintf(&b, "%s -> UNRESOLVED\n", name)
		}
	}
	fmt.Fprintf(&b, "\n# load classes\n")
	for _, class := range loadClasses {
		fmt.Fprintf(&b, "%s: %d\n", class, len(l.Load[class]))
	}
	fmt.Fprintf(&b, "boot drivers (union): %d\n", len(l.BootDrivers()))

	golden.Assert(t, "real-source-summary", b.String())
}

// sortedKeysNumeric returns a map's keys sorted numerically when they are integers,
// so the directory table renders in ID order (1, 2, 3, ... not 1, 10, 11).
func sortedKeysNumeric(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ni, ei := strconv.Atoi(keys[i])
		nj, ej := strconv.Atoi(keys[j])
		if ei == nil && ej == nil {
			return ni < nj
		}
		return keys[i] < keys[j]
	})
	return keys
}
