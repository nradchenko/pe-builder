// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"fmt"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
)

// system32DirID is the [WinntDirectories] directory ID for the system32 folder.
const system32DirID = "2"

// usKLID is the US-English keyboard layout, the one the neutral base ships.
const usKLID = "00000409"

// usLayoutDLL is the layout DLL base.inf already seeds for the US-English base. A locale that
// reuses it needs no copy of its own.
const usLayoutDLL = "kbdus.dll"

// preloadKey is the default user's list of keyboard layouts win32k loads at
// Win32-subsystem init.
const preloadKey = `Keyboard Layout\Preload`

// localKeyboard is a locale's keyboard layout: the layout DLL to install and its
// display name.
type localKeyboard struct {
	dll  string // layout DLL, e.g. kbdru.dll
	text string // layout display name, e.g. Russian
}

// keyboards maps a locale's keyboard-layout ID (KLID) to its layout. The US layout
// (00000409) is part of the neutral base, so only non-US locales are listed; a source
// whose locale is absent here keeps the US-English keyboard. This small table is the
// naive stand-in until the local layout is derived from the source's own registry.
var keyboards = map[string]localKeyboard{
	"00000419": {dll: "kbdru.dll", text: "Russian"},
	// Simplified Chinese types through an IME over the US physical layout, so it has
	// no layout DLL of its own and reuses the base's kbdus.dll. The source says the
	// same: its [nls] declares DefaultLayout = 00000804.
	"00000804": {dll: "kbdus.dll", text: "Chinese (Simplified) - US Keyboard"},
}

// localeFont is a font file a locale's own configuration depends on. name is the display
// name to register it under, or empty when the source's own hive config already names the
// file and only the file itself is missing.
type localeFont struct {
	file string // font file, e.g. simsun.ttc
	name string // registered display name, e.g. SimSun & NSimSun (TrueType)
}

// localeFonts maps a locale's KLID to the fonts its own configuration depends on. The
// base seeds the US-English set, so a locale whose fonts differ needs its own staged or
// the registry the hive fold brings in from the source points at files that are not
// there. A CJK locale needs both kinds:
//
//   - The scalable UI font. The source's hivesft.inf font-links Tahoma and Microsoft Sans
//     Serif to SIMSUN.TTC and has Winlogon preload it; registering it as well lets GDI
//     enumerate it directly.
//   - The bitmap fonts its own GRE_Initialize names in place of the US-English trio
//     base.inf seeds — for Simplified Chinese, svgafix/svgasys for FIXEDFON/FONTS and
//     vga936 for OEMFONT. OEMFONT is the console's font, and a DBCS console drawn with
//     the US-English vgaoem.fon has no glyphs for the script at all. The folded hive
//     already names these, so they need staging only.
//
// Like the keyboard table, this is the naive stand-in until the set is derived from the
// source's own GRE_Initialize and FontLink\SystemLink.
var localeFonts = map[string][]localeFont{
	"00000804": {
		{file: "simsun.ttc", name: "SimSun & NSimSun (TrueType)"},
		{file: "vga936.fon"},
		{file: "svgafix.fon"},
		{file: "svgasys.fon"},
	},
}

// localeConsole is a locale's console font selection: the face its code page needs, and
// the point height to ask for.
type localeConsole struct {
	codePage string // code page the face serves, e.g. 936
	face     string // face name, e.g. 新宋体 (NSimSun), simsun.ttc's fixed-pitch face
	height   uint32 // cell height in points, the high word of Console FontSize
}

// localeConsoles maps a locale's KLID to its console font. A DBCS locale needs one
// stated: the raster console font the base seeds carries no glyphs for the script, and
// while the locale's own bitmap font (see localeFonts) is staged and named by
// GRE_Initialize, nothing selects it for the console. Both halves of the TrueType path
// are set here — the face is added to the system list keyed by code page, and the
// default user's console is pointed at it, because the source's hivedef.inf ships
// FaceName empty and FontFamily zero, which leaves the console on the raster font.
//
// Neither value appears in the source's own hive INFs, and that is not evidence they are
// unneeded: Windows setup has a locale pass, which a PE never runs, that writes state no
// INF on the media contains. Both were confirmed load-bearing by boot — the system list
// alone leaves the console on the raster font, and the selection alone has no face to
// resolve.
var localeConsoles = map[string]localeConsole{
	// Height 16 rather than the 12 a Latin console uses: the face is full-width, and a
	// shorter cell leaves the glyphs stretched and narrow.
	"00000804": {codePage: "936", face: "新宋体", height: 16},
}

// localeActions applies the source's locale on top of the US-English base.
//
// The code-page tables and OEM console font the source's own txtsetup.sif [nls]
// section names are staged unconditionally, along with the registry entries naming each
// table by the number it serves — a table present but unnamed cannot be loaded, so both
// halves are how the locale takes effect. Gating them on the keyboard table would
// silently leave a source whose keyboard is unlisted with the English tables under a
// non-English locale. Which code page is *selected* (ACP/OEMCP) is a separate thing
// this does not set, and base.inf still leaves as text setup ships it.
//
// The keyboard layer needs a layout DLL this build can name, so it — and only it — is
// gated on the table. A source with no [nls] language, or one not yet listed, keeps the
// US-English keyboard.
func localeActions(l *layout.Layout) []action.Action {
	klid := l.NLS.KLID()
	out := nlsActions(l)
	out = append(out, fontActions(klid)...)
	out = append(out, consoleActions(klid)...)
	out = append(out, preloadActions()...)
	if kb, ok := keyboards[klid]; ok {
		out = append(out, keyboardActions(klid, kb)...)
	}
	return out
}

// fontActions stages the locale's own fonts and registers them so GDI can enumerate
// them (name → file in the Fonts dir), the same way base.inf registers the US-English UI
// fonts. Each file lands in the directory the source's layout names for it — the Fonts
// folder — so it carries no directory ID of its own. The registered name is the font's
// English one: the key maps a display name to a file and nothing resolves a font through
// it (FontLink names the file directly), so this keeps base's registry ASCII rather than
// reproducing the localized name.
func fontActions(klid string) []action.Action {
	var out []action.Action
	for _, f := range localeFonts[klid] {
		out = append(out, action.Action{Kind: action.CopyFile, Src: f.file})
		if f.name != "" {
			out = append(out, localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Fonts`, f.name, f.file))
		}
	}
	return out
}

// codePageKey is where the registry maps a code-page number to the table file that
// serves it. A table that is staged but not named here cannot be loaded.
const codePageKey = `ControlSet001\Control\Nls\CodePage`

// nlsActions stages the code-page tables (directory resolved from the source layout)
// and the OEM console font (system32) that the source names for itself, then registers
// each table under the number it serves. A locale may name one table for both code
// pages — Simplified Chinese names c_936.nls as ANSI and OEM alike — so each distinct
// name is staged and registered once. The OEM alternates a source names beside its own
// page are handled the same way: leaving one staged but unnamed, or named but unstaged,
// is the same unloadable state as leaving out the locale's own. An alternate the neutral
// base already seeds (Russian's 437) is copied twice — this cannot see what base staged —
// but the assembler overwrites with identical bytes, so only the write is wasted.
//
// Registering is not redundant with the hive fold. The source's hivesys.inf names only
// its *ANSI* table (Arabic media state 1256 = c_1256.nls) and leaves every other slot,
// its own OEM one included, present but empty — 720 = "". Windows fills the OEM slot in
// the locale pass a PE never runs (intl.inf's [nls.cpinstall.720]). Left empty, the
// staged table cannot be resolved and the console falls back to a built-in code page, so
// its text comes out as the wrong script. This bites every
// locale whose OEM code page differs from its ANSI one, so it is derived from the
// source's [nls] rather than listed per locale; where the two agree, the hive fold has
// already written the same value and this repeats it harmlessly.
func nlsActions(l *layout.Layout) []action.Action {
	var out []action.Action
	// The two are deduplicated separately and must be: a locale naming one table for
	// both code pages needs it staged once, but were the registration keyed on the file
	// too, a table named under two different numbers would leave the second number
	// unregistered — the very failure this registers to avoid.
	stagedFiles := map[string]bool{}
	registered := map[string]bool{}
	pages := []layout.CodePage{
		{File: l.NLS.AnsiCodepageFile, Number: l.NLS.AnsiCodepage},
		{File: l.NLS.OemCodepageFile, Number: l.NLS.OemCodepage},
	}
	pages = append(pages, l.NLS.OemAlternates...)
	for _, cp := range pages {
		if cp.File == "" {
			continue
		}
		if !stagedFiles[cp.File] {
			stagedFiles[cp.File] = true
			out = append(out, action.Action{Kind: action.CopyFile, Src: cp.File})
		}
		// A source that names a table but no number for it still gets the file; there
		// is nothing to key the registration on.
		if cp.Number != "" && !registered[cp.Number] {
			registered[cp.Number] = true
			out = append(out, localeReg(action.HiveSystem, codePageKey, cp.Number, cp.File))
		}
	}
	if l.NLS.OemHalFont != "" {
		out = append(out, action.Action{Kind: action.CopyFile, Src: l.NLS.OemHalFont, DirID: system32DirID})
	}
	return out
}

// consoleActions points the console at a face that can draw the locale's code page:
// it adds the face to the system list keyed by the code page, then selects it for the
// default user. FontFamily 0x36 (FF_MODERN | TMPF_TRUETYPE) is what marks the selection
// as a TrueType face rather than the raster default, and FontSize carries the cell
// height in its high word.
func consoleActions(klid string) []action.Action {
	c, ok := localeConsoles[klid]
	if !ok {
		return nil
	}
	const (
		trueTypeFamily = 0x36
		normalWeight   = 400
	)
	return []action.Action{
		localeReg(action.HiveSoftware, `Microsoft\Windows NT\CurrentVersion\Console\TrueTypeFont`, c.codePage, c.face),
		localeReg(action.HiveDefault, "Console", "FaceName", c.face),
		localeDword(action.HiveDefault, "Console", "FontFamily", trueTypeFamily),
		localeDword(action.HiveDefault, "Console", "FontWeight", normalWeight),
		localeDword(action.HiveDefault, "Console", "FontSize", c.height<<16),
	}
}

// preloadActions resets the default user's preloaded keyboard layouts to what this build
// actually ships, and owns the key so the result cannot depend on what the source folded
// in. The list is dropped first because a source's hivedef.inf may preload input methods
// the build does not stage: a Simplified Chinese one preloads three (全拼, 郑码, 智能 ABC),
// naming .ime files that are on the media but not in a PE. Preloading a layout whose IME
// cannot be loaded wedges the shell — an input context attaches on window activation, so
// it strikes when a modal dialog hands focus back rather than at logon, and only
// sometimes. US is the base layout; a known locale's own follows it in slot 2.
func preloadActions() []action.Action {
	return []action.Action{
		{Kind: action.DelReg, Hive: action.HiveDefault, Subkey: preloadKey},
		localeReg(action.HiveDefault, preloadKey, "1", usKLID),
	}
}

// keyboardActions installs a locale's keyboard layout DLL, preloads the layout after
// US (slot 2), and maps its KLID to the DLL so win32k can resolve the layout.
func keyboardActions(klid string, kb localKeyboard) []action.Action {
	var out []action.Action
	// A locale that types through an IME over the US layout names the base's own kbdus.dll,
	// which base.inf already seeds; copying it again stages the same file twice, since the tree
	// assembler does not deduplicate.
	if !strings.EqualFold(kb.dll, usLayoutDLL) {
		out = append(out, action.Action{Kind: action.CopyFile, Src: kb.dll, DirID: system32DirID})
	}
	return append(out,
		localeReg(action.HiveDefault, preloadKey, "2", klid),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\`+klid, "Layout File", strings.ToUpper(kb.dll)),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\`+klid, "Layout Text", kb.text),
	)
}

// localeDword builds a REG_DWORD AddReg action for one hive.
func localeDword(hive action.RegHive, subkey, value string, data uint32) action.Action {
	a := localeReg(hive, subkey, value, fmt.Sprintf("0x%x", data))
	a.RegType = action.RegDWord
	return a
}

// localeReg builds a REG_SZ AddReg action for one hive.
func localeReg(hive action.RegHive, subkey, value, data string) action.Action {
	return action.Action{
		Kind:      action.AddReg,
		Hive:      hive,
		Subkey:    subkey,
		ValueName: value,
		RegType:   action.RegSZ,
		Data:      data,
		HasValue:  true,
	}
}
