// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"testing"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// renameLayout models a medium that keeps a file under one name and installs it under
// another — the shape of a localized source's service-pack resource satellites.
func renameLayout() *layout.Layout {
	return &layout.Layout{
		Dirs: map[string]string{"1": "", "2": "system32"},
		Files: map[string]layout.FileEntry{
			"sprt0419.dll": {Name: "sprt0419.dll", TargetDir: "2", DestName: "xpsp2res.dll"},
			"plain.dll":    {Name: "plain.dll", TargetDir: "2"},
		},
	}
}

// The file must land under the name things load it by, not the name it is stored under.
// Placed as sprt0419.dll it is invisible: netshell asks for xpsp2res.dll and the medium
// looks as though it never carried one.
func TestOutputPathUsesTheDeclaredName(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "sprt0419.dll"}
	if got, want := OutputPath(a, renameLayout(), roots), "i386/system32/xpsp2res.dll"; got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

// A file the metadata declares no rename for keeps its own name — the common case, and the
// one every source that already worked depends on.
func TestOutputPathLeavesUnrenamedFilesAlone(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "plain.dll"}
	if got, want := OutputPath(a, renameLayout(), roots), "i386/system32/plain.dll"; got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

// A plugin naming its own destination outranks the medium's: asking for a specific name is
// deliberate, and a source's convention must not quietly redirect it.
func TestPluginRenameOutranksTheDeclaredName(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "sprt0419.dll", Rename: "chosen.dll"}
	if got, want := OutputPath(a, renameLayout(), roots), "i386/system32/chosen.dll"; got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

// declaredName must tolerate the absence of a layout: several callers place files with no
// source metadata in hand at all.
func TestDeclaredNameWithoutALayout(t *testing.T) {
	a := action.Action{Kind: action.CopyFile, Src: "sprt0419.dll"}
	if got := declaredName(nil, a, "sprt0419.dll"); got != "sprt0419.dll" {
		t.Errorf("declaredName(nil layout) = %q, want the name unchanged", got)
	}
}
