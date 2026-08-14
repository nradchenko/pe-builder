// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package layout

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/internal/repo"
	"github.com/nradchenko/pe-builder/source"
)

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
	src, err := source.Open(repo.SourceISO(t))
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

const sifHeader = "[Version]\nSignature = \"$Windows NT$\"\n\n"

// hivesysINF renders a hivesys.inf whose [AddReg] declares the given product-type string,
// or declares none at all when it is empty — the two shapes real media come in.
func hivesysINF(declared string) []byte {
	s := "[Version]\nSignature = \"$Windows NT$\"\n\n[AddReg]\n"
	if declared != "" {
		s += `HKLM,"SYSTEM\CurrentControlSet\Control\ProductOptions","ProductType",0x00000002,"` +
			declared + "\"\n"
	}
	// A line the reader must step over either way: same key, a different value.
	s += `HKLM,"SYSTEM\CurrentControlSet\Control\ProductOptions","ProductSuite",0x00010002,""` + "\n"
	return []byte(s)
}

// TestReadProductType covers the answer that decides whether a build reports itself as a
// workstation or a server — the only thing on the media separating XP Professional x64
// Edition from Server 2003 x64, which share both a build number and an architecture.
//
// The media state it two ways and the cases below pin which wins: hivesys.inf declares the
// product-type string outright (every XP-branch medium measured does), and a source that
// declares none falls back to the SKU in txtsetup.sif's [SetupData] ProductType.
func TestReadProductType(t *testing.T) {
	// declared and sku name the evidence each case is expected to settle on; the zero
	// ProductEvidence means nothing decided and the workstation default stands.
	declared := func(s string) ProductEvidence {
		return ProductEvidence{Answer: ProductAnswerDeclared, Value: s, Recognized: true}
	}
	sku := func(v string, recognized bool) ProductEvidence {
		return ProductEvidence{Answer: ProductAnswerSetupData, Value: v, Recognized: recognized}
	}

	tests := []struct {
		name     string
		section  string
		hivesys  []byte // nil = the source ships no hivesys.inf at all
		want     bool
		evidence ProductEvidence
	}{
		// Declared outright — the preferred answer, whatever the SKU says.
		{"declared WinNt is a workstation", "", hivesysINF("WinNt"), false, declared("WinNt")},
		{"declared ServerNT is a server", "", hivesysINF("ServerNT"), true, declared("ServerNT")},
		{"declared LanmanNT is a server", "", hivesysINF("LanmanNT"), true, declared("LanmanNT")},
		// XP media declare WinNt and state SKU 0; the two must not be able to disagree
		// into a server.
		{"declared workstation outranks the SKU", "[SetupData]\nProductType = 1\n", hivesysINF("WinNt"), false, declared("WinNt")},
		// An unknown string is not decoded as anything: fall through to the SKU, which is
		// then what the evidence names — the string decided nothing.
		{"unknown declared string falls back to the SKU", "[SetupData]\nProductType = 1\n", hivesysINF("EmbeddedNT"), true, sku("1", true)},
		// ...but with no SKU to fall back on, the unreadable declaration is still what the
		// media said. Recording nothing here would report media that answered as media
		// that were silent, which is the one thing this field exists to keep apart.
		{"unknown declared string with no SKU is still evidence", "", hivesysINF("EmbeddedNT"), false,
			ProductEvidence{Answer: ProductAnswerDeclared, Value: "EmbeddedNT"}},
		// Both answers present and neither readable: the SKU settled nothing, so it does
		// not displace the declaration. Reporting only the SKU would name the weaker of
		// two statements and lose that the media declared anything at all.
		{"unknown declared string with an unmeasured SKU keeps the declaration", "[SetupData]\nProductType = 9\n",
			hivesysINF("EmbeddedNT"), false, ProductEvidence{Answer: ProductAnswerDeclared, Value: "EmbeddedNT"}},

		// Server 2003 media declare no ProductOptions at all, so the SKU decides.
		{"silent media, SKU 1 is a server", "[SetupData]\nProductType = 1\n", hivesysINF(""), true, sku("1", true)},
		{"silent media, SKU 6 is a server", "[SetupData]\nProductType = 6\n", hivesysINF(""), true, sku("6", true)},
		{"silent media, SKU 0 is a workstation", "[SetupData]\nProductType = 0\n", hivesysINF(""), false, sku("0", true)},
		// The field names a SKU, not a yes/no: XP Starter Edition media states 4, and
		// reading any non-zero as "server" brands that build ServerNT — which the kernel
		// then reports, putting the server banner on the shell's splash.
		{"silent media, SKU 4 is a workstation", "[SetupData]\nProductType = 4\n", hivesysINF(""), false, sku("4", true)},
		// A SKU nobody has measured is not guessed into a server, and the evidence has to
		// say so: the verdict is a default, not a reading.
		{"silent media, unmeasured SKU falls back to workstation", "[SetupData]\nProductType = 9\n", hivesysINF(""), false, sku("9", false)},

		// A source that states nothing anywhere must not be guessed into a server:
		// workstation is what every build produced before this was read at all.
		{"no hivesys.inf and no SKU", "", nil, false, ProductEvidence{}},
		{"absent value falls back to workstation", "[SetupData]\nMinimumMemory = 32\n", hivesysINF(""), false, ProductEvidence{}},
		// Unparsable data must not read as non-zero and flip the product silently.
		{"unparsable SKU falls back to workstation", "[SetupData]\nProductType = server\n", hivesysINF(""), false, ProductEvidence{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := fstest.MapFS{
				"i386/txtsetup.sif": {Data: []byte(sifHeader + syntheticTxtsetup + "\n" + tt.section)},
				"i386/layout.inf":   {Data: []byte(syntheticLayout)},
			}
			if tt.hivesys != nil {
				src["i386/hivesys.inf"] = &fstest.MapFile{Data: tt.hivesys}
			}
			l, err := Parse(src, source.Identity{Arch: "i386", Build: 2600})
			if err != nil {
				t.Fatal(err)
			}
			if l.ServerProduct != tt.want {
				t.Errorf("ServerProduct = %v, want %v", l.ServerProduct, tt.want)
			}
			if l.ProductEvidence != tt.evidence {
				t.Errorf("ProductEvidence = %+v, want %+v", l.ProductEvidence, tt.evidence)
			}
		})
	}
}

// TestRealSourceIsWorkstation pins the real XP source through the whole parse path, so the
// synthetic cases above cannot drift from what actual media says.
func TestRealSourceIsWorkstation(t *testing.T) {
	src, err := source.Open(repo.SourceISO(t))
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
	if l.ServerProduct {
		t.Errorf("XP media reported as a server product; want workstation")
	}
	// Real XP media declare the product type outright, so the verdict must come from the
	// declaration and not from the SKU fallback: reaching the same answer down the weaker
	// path would hide a regression in reading the stronger one.
	want := ProductEvidence{Answer: ProductAnswerDeclared, Value: "WinNt", Recognized: true}
	if l.ProductEvidence != want {
		t.Errorf("ProductEvidence = %+v, want %+v", l.ProductEvidence, want)
	}
}
