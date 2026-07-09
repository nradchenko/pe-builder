// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
)

// system32DirID is the [WinntDirectories] directory ID for the system32 folder.
const system32DirID = "2"

// localKeyboard is a locale's keyboard layout: the layout DLL to install and its
// display name.
type localKeyboard struct {
	dll  string // layout DLL, e.g. kbdru.dll
	text string // layout display name, e.g. Russian
}

// keyboards maps a locale's keyboard-layout ID (KLID) to its layout. The US layout
// (00000409) is part of the neutral base, so only non-US locales are listed; a source
// whose locale is absent here keeps the US-English base. This small table is the
// naive stand-in until the local layout is derived from the source's own registry.
var keyboards = map[string]localKeyboard{
	"00000419": {dll: "kbdru.dll", text: "Russian"},
}

// localeActions applies the source's locale on top of the US-English base. It
// installs the ANSI and OEM code-page tables and the OEM console font that the
// source's own txtsetup.sif [nls] section names, plus the local keyboard layout DLL
// and the registry that selects it. It fires only for a locale this build knows (one
// in the keyboard table); a source with no [nls] language, or one not yet listed,
// keeps the US-English base unchanged.
func localeActions(l *layout.Layout) []action.Action {
	klid := l.NLS.KLID()
	kb, ok := keyboards[klid]
	if !ok {
		return nil
	}

	var out []action.Action
	// Code-page tables (directory resolved from the source layout) and the OEM
	// console font (system32), all named by the source itself.
	for _, name := range []string{l.NLS.AnsiCodepageFile, l.NLS.OemCodepageFile} {
		if name != "" {
			out = append(out, action.Action{Kind: action.CopyFile, Src: name})
		}
	}
	if l.NLS.OemHalFont != "" {
		out = append(out, action.Action{Kind: action.CopyFile, Src: l.NLS.OemHalFont, DirID: system32DirID})
	}
	// The local keyboard layout DLL.
	out = append(out, action.Action{Kind: action.CopyFile, Src: kb.dll, DirID: system32DirID})
	// Preload the local layout after US (slot 2), and map its KLID to the DLL so
	// win32k can resolve the layout.
	out = append(out,
		localeReg(action.HiveDefault, `Keyboard Layout\Preload`, "2", klid),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\`+klid, "Layout File", strings.ToUpper(kb.dll)),
		localeReg(action.HiveSystem, `ControlSet001\Control\Keyboard Layouts\`+klid, "Layout Text", kb.text),
	)
	return out
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
