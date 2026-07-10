// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/action"
)

// byName resolves a copy action's "output path" for tests as its target name, enough
// to exercise files.exclude matching without a real source layout.
func byTarget(a action.Action) string {
	if a.Rename != "" {
		return a.Rename
	}
	return a.Src
}

func TestCompileOriginStamping(t *testing.T) {
	// "a" ships a.exe (ShipWithPlugin) and also copies winfile.dll from the Windows
	// source (no ShipWithPlugin). "b" ships no files (nil FS).
	a := &Discovered{
		Name: "a",
		FS:   fstest.MapFS{"a.exe": &fstest.MapFile{Data: []byte("A")}},
		Units: []*Plugin{{Name: "a", Actions: []action.Action{
			{Kind: action.CopyFile, Src: "a.exe", DirID: "2", ShipWithPlugin: true},
			{Kind: action.CopyFile, Src: "winfile.dll", DirID: "2"}, // from the source
			{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: "A"},
		}}},
	}
	b := &Discovered{
		Name:  "b",
		Units: []*Plugin{{Name: "b", Actions: []action.Action{{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: "B"}}}},
	}

	acts, origins, err := Compile([]Selected{{Plugin: a}, {Plugin: b}}, byTarget)
	if err != nil {
		t.Fatal(err)
	}

	// Only the ShipWithPlugin copy is stamped with an Origin.
	want := []struct {
		kind   action.Kind
		origin string
	}{
		{action.CopyFile, "a"}, // a.exe, shipped
		{action.CopyFile, ""},  // winfile.dll, from the source
		{action.AddReg, ""},
		{action.AddReg, ""},
	}
	if len(acts) != len(want) {
		t.Fatalf("got %d actions, want %d", len(acts), len(want))
	}
	for i, w := range want {
		if acts[i].Kind != w.kind || acts[i].Origin != w.origin {
			t.Errorf("acts[%d] = {%s, origin=%q}, want {%s, origin=%q}",
				i, acts[i].Kind, acts[i].Origin, w.kind, w.origin)
		}
	}
	if _, ok := origins["a"]; !ok {
		t.Error("origins missing entry for a")
	}
	if _, ok := origins["b"]; ok {
		t.Error("origins has an entry for b, which ships no files")
	}
}

func TestCompileBuiltPlugin(t *testing.T) {
	// A copy of a file the plugin's [Build] produced resolves from the build output,
	// not the Windows source and not the plugin's shipped files.
	d := &Discovered{
		Name: "demo",
		Units: []*Plugin{{Name: "demo", Actions: []action.Action{
			{Kind: action.CopyFile, Src: "tool.exe", DirID: "2"}, // produced, not ShipWithPlugin
		}}},
	}
	built := &Built{
		Dir:      fstest.MapFS{"tool.exe": &fstest.MapFile{Data: []byte("bin")}},
		Produces: map[string]bool{"tool.exe": true},
	}
	acts, origins, err := Compile([]Selected{{Plugin: d, Built: built}}, byTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Origin != "build:demo" {
		t.Fatalf("acts = %+v, want the copy stamped with origin build:demo", acts)
	}
	if _, ok := origins["build:demo"]; !ok {
		t.Error("origins missing the build output for demo")
	}
}

func TestCompileExcludeSuppresses(t *testing.T) {
	d := &Discovered{
		Name: "p",
		FS:   fstest.MapFS{"keep.exe": &fstest.MapFile{Data: []byte("k")}, "drop.exe": &fstest.MapFile{Data: []byte("d")}},
		Units: []*Plugin{{Name: "p", Actions: []action.Action{
			{Kind: action.CopyFile, Src: "keep.exe", DirID: "2", ShipWithPlugin: true},
			{Kind: action.CopyFile, Src: "drop.exe", DirID: "2", ShipWithPlugin: true},
		}}},
	}
	sel := []Selected{{Plugin: d, Override: Override{Exclude: []string{"drop.exe"}}}}

	acts, _, err := Compile(sel, byTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Src != "keep.exe" {
		t.Fatalf("acts = %+v, want only keep.exe (drop.exe excluded)", acts)
	}
}

func TestCompileOverrideAdditions(t *testing.T) {
	d := &Discovered{Name: "p", Units: []*Plugin{{Name: "p"}}}
	sel := []Selected{{Plugin: d, Override: Override{
		Include:   []string{"i386/system32/extra.dll"},
		RegSet:    map[string]string{`HKLM\SOFTWARE\Foo\Bar`: "baz"},
		RegDelete: []string{`HKLM\SYSTEM\Setup\Old`},
	}}}

	acts, _, err := Compile(sel, byTarget)
	if err != nil {
		t.Fatal(err)
	}

	var gotInclude, gotSet, gotDelete bool
	for _, a := range acts {
		switch {
		case a.Kind == action.CopyFile && a.Src == "extra.dll" && a.Dest == "i386/system32/extra.dll":
			gotInclude = true
		case a.Kind == action.AddReg && a.Hive == action.HiveSoftware && a.Subkey == "Foo" && a.ValueName == "Bar" && a.Data == "baz":
			gotSet = true
		case a.Kind == action.DelReg && a.Hive == action.HiveSystem && a.Subkey == "Setup" && a.ValueName == "Old":
			gotDelete = true
		}
	}
	if !gotInclude {
		t.Error("include did not produce the expected source copy")
	}
	if !gotSet {
		t.Error("registry.set did not produce the expected AddReg")
	}
	if !gotDelete {
		t.Error("registry.delete did not produce the expected DelReg")
	}
}
