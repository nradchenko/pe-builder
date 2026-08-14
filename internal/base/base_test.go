// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// fixtureLayout provides just enough of a source layout to derive boot drivers.
func fixtureLayout() *layout.Layout {
	return &layout.Layout{
		Dirs: map[string]string{"2": "system32", "4": "system32/drivers"},
		Files: map[string]layout.FileEntry{
			"atapi.sys": {Name: "atapi.sys", TargetDir: "4"},
			"ntfs.sys":  {Name: "ntfs.sys", TargetDir: "4"},
			"hal.dll":   {Name: "hal.dll", TargetDir: "2"},
		},
		Load: map[string][]string{
			"Hal":         {"hal.dll", "halacpi.dll"},
			"FileSystems": {"ntfs.sys", "fastfat.sys"},
			"SCSI":        {"atapi.sys"},
		},
		NLS: layout.NLS{
			AnsiCodepageFile: "c_1251.nls",
			AnsiCodepage:     "1251",
			OemCodepageFile:  "c_866.nls",
			OemCodepage:      "866",
			OemAlternates:    []layout.CodePage{{File: "c_437.nls", Number: "437"}},
			OemHalFont:       "vga866.fon",
			LangID:           "0419",
		},
	}
}

func TestLocaleActions(t *testing.T) {
	got := localeActions(fixtureLayout())
	want := []action.Action{
		{Kind: action.CopyFile, Src: "c_1251.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "1251", "c_1251.nls"),
		{Kind: action.CopyFile, Src: "c_866.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "866", "c_866.nls"),
		{Kind: action.CopyFile, Src: "c_437.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "437", "c_437.nls"),
		{Kind: action.CopyFile, Src: "vga866.fon", DirID: "2"},
		{Kind: action.DelReg, Hive: action.HiveDefault, Subkey: `Keyboard Layout\Preload`},
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "1", "00000409"),
		{Kind: action.CopyFile, Src: "kbdru.dll", DirID: "2"},
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "2", "00000419"),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\00000419`, "Layout File", "KBDRU.DLL"),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\00000419`, "Layout Text", "Russian"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("localeActions mismatch (-want +got):\n%s", diff)
	}
}

// TestLocaleActionsUnknownKeyboard checks a source whose locale is not in the keyboard
// table still stages its own code-page tables and console font. Staging those is how the
// locale takes effect at all, so it must not depend on knowing the keyboard; only the
// keyboard layer is skipped, leaving the US-English one in place.
func TestLocaleActionsUnknownKeyboard(t *testing.T) {
	l := fixtureLayout()
	l.NLS.LangID = "0407" // German: not in the keyboard table
	got := localeActions(l)
	want := []action.Action{
		{Kind: action.CopyFile, Src: "c_1251.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "1251", "c_1251.nls"),
		{Kind: action.CopyFile, Src: "c_866.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "866", "c_866.nls"),
		{Kind: action.CopyFile, Src: "c_437.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "437", "c_437.nls"),
		{Kind: action.CopyFile, Src: "vga866.fon", DirID: "2"},
		{Kind: action.DelReg, Hive: action.HiveDefault, Subkey: `Keyboard Layout\Preload`},
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "1", "00000409"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("localeActions for unlisted keyboard mismatch (-want +got):\n%s", diff)
	}
}

// TestLocaleActionsSharedCodepage covers the shape a DBCS source has: one table named as
// both the ANSI and the OEM code page, which must be staged once rather than twice. The
// values are the real ones read from Simplified Chinese XP SP3 and Server 2003 media,
// whose [nls] sections are identical, not invented ones.
func TestLocaleActionsSharedCodepage(t *testing.T) {
	l := fixtureLayout()
	l.NLS = layout.NLS{
		AnsiCodepageFile: "c_936.nls",
		AnsiCodepage:     "936",
		OemCodepageFile:  "c_936.nls",
		OemCodepage:      "936",
		OemHalFont:       "vgaoem.fon",
		LangID:           "0804",
	}
	got := localeActions(l)
	want := []action.Action{
		{Kind: action.CopyFile, Src: "c_936.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "936", "c_936.nls"),
		{Kind: action.CopyFile, Src: "vgaoem.fon", DirID: "2"},
		{Kind: action.CopyFile, Src: "simsun.ttc"},
		localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Fonts`, "SimSun & NSimSun (TrueType)", "simsun.ttc"),
		{Kind: action.CopyFile, Src: "vga936.fon"},
		{Kind: action.CopyFile, Src: "svgafix.fon"},
		{Kind: action.CopyFile, Src: "svgasys.fon"},
		localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Console\TrueTypeFont`, "936", "新宋体"),
		localeReg(action.HiveDefault, "Console", "FaceName", "新宋体"),
		localeDword(action.HiveDefault, "Console", "FontFamily", 0x36),
		localeDword(action.HiveDefault, "Console", "FontWeight", 400),
		localeDword(action.HiveDefault, "Console", "FontSize", 16<<16),
		{Kind: action.DelReg, Hive: action.HiveDefault, Subkey: `Keyboard Layout\Preload`},
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "1", "00000409"),
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "2", "00000804"),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\00000804`, "Layout File", "KBDUS.DLL"),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\00000804`, "Layout Text", "Chinese (Simplified) - US Keyboard"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("localeActions for shared ANSI/OEM code page mismatch (-want +got):\n%s", diff)
	}
}

// TestLocaleActionsConsoleFaceWithoutKeyboard covers a single-byte locale that needs a
// console face but no keyboard: Arabic states distinct ANSI and OEM code pages, and its
// console is keyed by the OEM one. Its KLID is absent from the keyboard table, so the
// layout stays US — the face and the keyboard are independent. The values are the real
// ones read from Arabic XP Professional SP3 media, not invented ones.
func TestLocaleActionsConsoleFaceWithoutKeyboard(t *testing.T) {
	l := fixtureLayout()
	l.NLS = layout.NLS{
		AnsiCodepageFile: "c_1256.nls",
		AnsiCodepage:     "1256",
		OemCodepageFile:  "c_720.nls",
		OemCodepage:      "720",
		OemAlternates:    []layout.CodePage{{File: "c_850.nls", Number: "850"}},
		OemHalFont:       "vgaoem.fon",
		LangID:           "0401",
	}
	got := localeActions(l)
	want := []action.Action{
		{Kind: action.CopyFile, Src: "c_1256.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "1256", "c_1256.nls"),
		{Kind: action.CopyFile, Src: "c_720.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "720", "c_720.nls"),
		{Kind: action.CopyFile, Src: "c_850.nls"},
		localeReg(action.HiveSystem, `ControlSet001\Control\Nls\CodePage`, "850", "c_850.nls"),
		{Kind: action.CopyFile, Src: "vgaoem.fon", DirID: "2"},
		{Kind: action.CopyFile, Src: "cour.ttf"},
		localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Console\TrueTypeFont`, "720", "Courier New"),
		localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Console\TrueTypeFont`, "00", "Courier New"),
		localeReg(action.HiveDefault, "Console", "FaceName", "Courier New"),
		localeDword(action.HiveDefault, "Console", "FontFamily", 0x36),
		localeDword(action.HiveDefault, "Console", "FontWeight", 400),
		localeDword(action.HiveDefault, "Console", "FontSize", 16<<16),
		{Kind: action.DelReg, Hive: action.HiveDefault, Subkey: `Keyboard Layout\Preload`},
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "1", "00000409"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("localeActions for a console face without a keyboard mismatch (-want +got):\n%s", diff)
	}
}

func TestDriverActions(t *testing.T) {
	got := driverActions(fixtureLayout())
	want := []action.Action{
		{Kind: action.CopyFile, Src: "hal.dll", Optional: true},
		{Kind: action.CopyFile, Src: "halacpi.dll", Optional: true},
		{Kind: action.CopyFile, Src: "atapi.sys", Optional: true},
		{Kind: action.CopyFile, Src: "ntfs.sys", Optional: true},
		{Kind: action.CopyFile, Src: "fastfat.sys", Optional: true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("driverActions mismatch (-want +got):\n%s", diff)
	}
}

// TestActions compiles the embedded base.inf plus fixture-derived drivers and diffs
// the whole action set against a golden rendering, so a change to base.inf is a
// visible, reviewable diff.
func TestActions(t *testing.T) {
	acts, err := Actions(nil, fixtureLayout(), source.Identity{Build: 2600, Arch: "i386"})
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "base-actions", renderActions(acts))
}

// TestActionsAlwaysSetMinint asserts the property the golden cannot: whatever the
// architecture, the compiled base carries the loader options that make the output a
// PE. /minint is the single edit that distinguishes a PE from ordinary setup, and it
// now lives in architecture-scoped sections — so a source whose architecture selects
// no section would produce a bootable-looking tree that is not a PE at all, with
// nothing failing. This is what makes that unrepresentable.
func TestActionsAlwaysSetMinint(t *testing.T) {
	for _, arch := range []string{"i386", "amd64", ""} {
		acts, err := Actions(nil, fixtureLayout(), source.Identity{Build: 2600, Arch: arch})
		if err != nil {
			t.Fatalf("arch %q: %v", arch, err)
		}
		var opts string
		for _, a := range acts {
			if a.Kind == action.SetValue && strings.EqualFold(a.LineKey, "osloadoptions") {
				opts = a.LineData
			}
		}
		switch {
		case opts == "":
			t.Errorf("arch %q: no osloadoptions in the compiled base", arch)
		case !strings.Contains(opts, "/minint"):
			t.Errorf("arch %q: osloadoptions lacks /minint: %s", arch, opts)
		case !strings.Contains(opts, "/hal="):
			t.Errorf("arch %q: osloadoptions names no HAL: %s", arch, opts)
		}
	}
}

// renderActions serializes an action list to a stable, readable form for golden
// diffing: one line per action with only the fields relevant to its kind.
func renderActions(acts []action.Action) string {
	var b strings.Builder
	for _, a := range acts {
		switch a.Kind {
		case action.CopyFile:
			fmt.Fprintf(&b, "copy %s dir=%q rename=%q area=%s flags=%s\n",
				a.Src, a.DirID, a.Rename, a.Area, copyFlags(a))
		case action.Mkdir:
			fmt.Fprintf(&b, "mkdir id=%s path=%q area=%s fromRoot=%v always=%v\n",
				a.DirID, a.Path, a.Area, a.FromRoot, a.AlwaysCreate)
		case action.AddReg:
			fmt.Fprintf(&b, "addreg hive=%s type=0x%x subkey=%q value=%q data=%q\n",
				a.Hive, a.RegType, a.Subkey, a.ValueName, a.Data)
		case action.DelReg:
			fmt.Fprintf(&b, "delreg hive=%s subkey=%q value=%q\n", a.Hive, a.Subkey, a.ValueName)
		case action.SetValue:
			fmt.Fprintf(&b, "setvalue file=%q section=%q key=%q data=%q\n",
				a.TargetFile, a.TargetSection, a.LineKey, a.LineData)
		default:
			fmt.Fprintf(&b, "%s\n", a.Kind)
		}
	}
	return b.String()
}

func copyFlags(a action.Action) string {
	var f []string
	if a.NoDecompress {
		f = append(f, "nodecompress")
	}
	if a.Optional {
		f = append(f, "optional")
	}
	if a.Uppercase {
		f = append(f, "uppercase")
	}
	if a.ShipWithPlugin {
		f = append(f, "shipwithplugin")
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, "|")
}
