// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// aliasLayout models a medium that stores a file under one name and declares another as the
// name it installs as. The stored entry carries a directory of its own, which is the point
// of the placement test below: the requested name is declared nowhere, so nothing but that
// entry can say where the file belongs.
func aliasLayout() *layout.Layout {
	return &layout.Layout{
		Dirs: map[string]string{"1": "", "2": "system32", "103": "system32/spool"},
		Files: map[string]layout.FileEntry{
			"hello.txt":    {Name: "hello.txt", TargetDir: "103", DestName: "greeting.txt"},
			"sprt0419.dll": {Name: "sprt0419.dll", TargetDir: "103", DestName: "xpsp2res.dll"},
		},
	}
}

// A file addressed by its installed name must be reachable inside a cabinet. The wanted set
// is fixed before any file is placed and a cabinet yields only what that set names, so an
// alias resolved later finds an empty resolver however well the name resolves elsewhere.
func TestAliasResolvesACabinetOnlyMember(t *testing.T) {
	cab, err := os.ReadFile(filepath.Join("testdata", "lzx-single.cab"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := source.Identity{Arch: "i386"}.Roots()
	// hello.txt exists only inside the cabinet; the copy asks for the name it installs as.
	src := fstest.MapFS{"i386/driver.cab": {Data: cab}}
	out := t.TempDir()

	acts := []action.Action{{Kind: action.CopyFile, Src: "greeting.txt"}}
	if _, err := Assemble(context.Background(), acts, aliasLayout(), src, roots, nil, out, nil); err != nil {
		t.Fatalf("a cabinet-only file asked for by its installed name should resolve: %v", err)
	}
	assertFile(t, out, "i386/system32/spool/greeting.txt", want)
}

// And it must land in the directory the entry that declares it names. The requested name is
// declared nowhere, so resolving the directory by that name falls back to system32 and puts
// the file somewhere nothing will look.
func TestAliasUsesTheDeclaringEntrysDirectory(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "xpsp2res.dll"}
	if got, want := OutputPath(a, aliasLayout(), roots), "i386/system32/spool/xpsp2res.dll"; got != want {
		t.Errorf("OutputPath = %q, want %q — the directory comes from the entry that declares it", got, want)
	}
}

// A plugin's own file never came from the Windows source, so the medium's declarations do
// not describe it — not the name it installs as, and not where it belongs either.
//
// No DirID here, deliberately: carrying one bypasses the directory lookup entirely, so a
// test that sets it cannot see whether the lookup follows the alias table. That is how this
// escaped the first time.
func TestPluginShippedFileIsNotRenamedByTheMedium(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "greeting.txt", Origin: "someplugin"}
	// greeting.txt is what hello.txt installs as, so a medium-driven lookup would place
	// this in the declaring entry's directory. A plugin's file goes to the default instead.
	if got, want := OutputPath(a, aliasLayout(), roots), "i386/system32/greeting.txt"; got != want {
		t.Errorf("OutputPath = %q, want %q — a plugin-shipped file is not placed by the medium", got, want)
	}
}

// And the name half of the same invariant, with a file the medium does declare a rename for.
func TestPluginShippedFileKeepsItsOwnName(t *testing.T) {
	roots := source.Identity{Arch: "i386"}.Roots()
	a := action.Action{Kind: action.CopyFile, Src: "hello.txt", Origin: "someplugin", DirID: "2"}
	if got, want := OutputPath(a, aliasLayout(), roots), "i386/system32/hello.txt"; got != want {
		t.Errorf("OutputPath = %q, want %q — a plugin-shipped file keeps its own name", got, want)
	}
}
