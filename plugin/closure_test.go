// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"testing"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// TestParseClosure parses a [Closure] section: one bare module name per line, seeding
// the plugin's computed file set.
func TestParseClosure(t *testing.T) {
	src := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"GUI\"\nEnable=0\n" +
		"[Closure]\nshell32.dll\ncomctl32.dll\nole32.dll\n"
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("win32-gui", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shell32.dll", "comctl32.dll", "ole32.dll"}
	if !reflect.DeepEqual(p.Closure, want) {
		t.Errorf("Closure = %v, want %v", p.Closure, want)
	}
}

// TestParseNoClosure confirms the section is purely additive: a plugin without one has
// no seeds, so the engine computes no closure and uses its [SourceDisksFiles] as before.
func TestParseNoClosure(t *testing.T) {
	src := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"P\"\nEnable=1\n"
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("p", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Closure) != 0 {
		t.Errorf("Closure = %v, want empty", p.Closure)
	}
}

// TestCompileClosureNilSource confirms that with no indexed source, a [Closure] plugin
// reduces to its explicit actions rather than failing — the computed set is simply
// skipped (used by tests that have no real source to index).
func TestCompileClosureNilSource(t *testing.T) {
	d := &Discovered{
		Name: "win32-gui",
		Units: []*Plugin{{
			Name:    "win32-gui",
			Closure: []string{"shell32.dll"},
			Actions: []action.Action{{Kind: action.CopyFile, Src: "seed.dll"}},
		}},
	}
	acts, _, err := Compile([]Selected{{Plugin: d}}, nil, func(a action.Action) string { return a.Src })
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Src != "seed.dll" {
		t.Errorf("acts = %+v, want only the explicit seed.dll (closure skipped with nil source)", acts)
	}
}

// TestClosureSeedsGathersUnits confirms a bundle's [Closure] seeds are gathered across
// every unit, like Actions.
func TestClosureSeedsGathersUnits(t *testing.T) {
	d := &Discovered{
		Name: "bundle",
		Units: []*Plugin{
			{Name: "bundle", Closure: []string{"a.dll"}},
			{Name: "extra", Closure: []string{"b.dll"}},
		},
	}
	if got, want := d.ClosureSeeds(), []string{"a.dll", "b.dll"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ClosureSeeds = %v, want %v", got, want)
	}
}
