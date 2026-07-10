// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/action"
)

func TestCompile(t *testing.T) {
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
		Name: "b",
		Units: []*Plugin{{Name: "b", Actions: []action.Action{
			{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: "B"},
		}}},
	}

	acts, origins := Compile([]*Discovered{a, b})

	// Actions flatten in plugin order; only the ShipWithPlugin copy is stamped with an
	// Origin — the source copy and the registry actions keep an empty Origin.
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

	// The plugin that ships files has an origin entry; the one that ships none does not.
	if _, ok := origins["a"]; !ok {
		t.Error("origins missing entry for a")
	}
	if _, ok := origins["b"]; ok {
		t.Error("origins has an entry for b, which ships no files")
	}
}
