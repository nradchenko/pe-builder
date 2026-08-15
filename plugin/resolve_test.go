// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"strings"
	"testing"
)

// dep builds a Discovered whose main unit carries an [Update] with the given id and
// depends list. Its [PEBuilder] Enable defaults to false, so tests that see it pulled
// into a build prove auto-enable ignores a dependency's own default.
func dep(name, id string, depends ...string) *Discovered {
	return &Discovered{
		Name:  name,
		Units: []*Plugin{{Name: name, Update: Update{Present: true, ID: id, Depends: depends}}},
	}
}

// resolve runs Resolve and returns the resulting names, failing the test on error.
func resolve(t *testing.T, discovered []*Discovered, enabled, disabled []string) []string {
	t.Helper()
	got, err := Resolve(discovered, enabled, disabled)
	if err != nil {
		t.Fatalf("Resolve(%v) errored: %v", enabled, err)
	}
	return orderedNames(got)
}

func TestResolveChainAutoEnables(t *testing.T) {
	// Enabling only the dependent pulls in the dependency, placed first.
	disc := []*Discovered{dep("explorer-shell", "", "win32-gui"), dep("win32-gui", "")}
	got := resolve(t, disc, []string{"explorer-shell"}, nil)
	if want := []string{"win32-gui", "explorer-shell"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (dependency auto-enabled, deps-first)", got, want)
	}
}

func TestResolveTransitiveChain(t *testing.T) {
	// A → B → C orders C, B, A.
	disc := []*Discovered{dep("a", "", "b"), dep("b", "", "c"), dep("c", "")}
	got := resolve(t, disc, []string{"a"}, nil)
	if want := []string{"c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (transitive deps-first)", got, want)
	}
}

func TestResolveDiamondDeDups(t *testing.T) {
	// A → C, B → C: C is enabled once, before both dependents.
	disc := []*Discovered{dep("a", "", "c"), dep("b", "", "c"), dep("c", "")}
	got := resolve(t, disc, []string{"a", "b"}, nil)
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (diamond de-dups, C first)", got, want)
	}
}

func TestResolveDependsByID(t *testing.T) {
	// A depends target may name another plugin's [Update] id, not its folder name.
	disc := []*Discovered{dep("tool", "tool", "core"), dep("core-plugin", "core")}
	got := resolve(t, disc, []string{"tool"}, nil)
	if want := []string{"core-plugin", "tool"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (depends resolved by id)", got, want)
	}
}

func TestResolveDefaultEnableIgnored(t *testing.T) {
	// The dependency's own [PEBuilder] Enable is false (dep() leaves it zero); it is
	// still pulled in because a dependent needs it.
	runtime := dep("win32-gui", "")
	if runtime.Main().DeclaredEnable {
		t.Fatal("test setup: win32-gui should declare Enable=false")
	}
	disc := []*Discovered{dep("notepad", "", "win32-gui"), runtime}
	got := resolve(t, disc, []string{"notepad"}, nil)
	if want := []string{"win32-gui", "notepad"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (default-disabled dependency still enabled)", got, want)
	}
}

func TestResolveConventionTieBreak(t *testing.T) {
	// With no depends edges, Resolve reduces byte-for-byte to Order (the regression
	// guard): the '!'/'z_' convention orders otherwise-independent plugins.
	names := []string{"z_late", "mid", "!early", "alpha", "zz9", "Zebra"}
	disc := make([]*Discovered, len(names))
	for i, n := range names {
		disc[i] = dep(n, "")
	}
	got := resolve(t, disc, names, nil)

	oracle := named(names...)
	Order(oracle)
	want := orderedNames(oracle)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve (no edges) = %v, want %v (must equal Order)", got, want)
	}
}

func TestResolveDependsOverridesConvention(t *testing.T) {
	// A '!early' plugin that depends on a 'z_late' one: the dependency loads first even
	// though the naming convention would order it last.
	disc := []*Discovered{dep("!early", "", "z_late"), dep("z_late", "")}
	got := resolve(t, disc, []string{"!early"}, nil)
	if want := []string{"z_late", "!early"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %v, want %v (depends wins over convention)", got, want)
	}
}

func TestResolveEnabledNotFound(t *testing.T) {
	disc := []*Discovered{dep("alpha", "")}
	_, err := Resolve(disc, []string{"alpha", "ghost", "phantom"}, nil)
	if err == nil {
		t.Fatal("enabling an undiscovered plugin returned no error")
	}
	for _, name := range []string{"ghost", "phantom"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name missing plugin %q", err, name)
		}
	}
}

func TestResolveUnmetDependency(t *testing.T) {
	disc := []*Discovered{dep("tool", "", "ramdisk")}
	_, err := Resolve(disc, []string{"tool"}, nil)
	if err == nil {
		t.Fatal("unmet dependency returned no error")
	}
	if !strings.Contains(err.Error(), "tool") || !strings.Contains(err.Error(), "ramdisk") {
		t.Errorf("error %q should name both the dependent and the missing dependency", err)
	}
}

func TestResolveDisabledDependencyConflict(t *testing.T) {
	// Enabling the dependent while the dependency is explicitly disabled is a conflict,
	// never a silent override.
	disc := []*Discovered{dep("explorer-shell", "", "win32-gui"), dep("win32-gui", "")}
	_, err := Resolve(disc, []string{"explorer-shell"}, []string{"win32-gui"})
	if err == nil {
		t.Fatal("required-but-disabled dependency returned no error")
	}
	if !strings.Contains(err.Error(), "explorer-shell") || !strings.Contains(err.Error(), "win32-gui") {
		t.Errorf("error %q should name both the dependent and the disabled dependency", err)
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error %q should explain the dependency is disabled", err)
	}
}

func TestResolveDisabledByIDConflict(t *testing.T) {
	// depends is matched by name or [Update] id; disabling the dependency by its id
	// must be honored too, not silently overridden. Here the dependency's dir name is
	// "core-plugin" but it is disabled (and depended on) by its id "core".
	disc := []*Discovered{dep("tool", "", "core"), dep("core-plugin", "core")}
	_, err := Resolve(disc, []string{"tool"}, []string{"core"})
	if err == nil {
		t.Fatal("dependency disabled by id returned no error")
	}
	if !strings.Contains(err.Error(), "tool") || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error %q should name the dependent and explain the dependency is disabled", err)
	}
}

func TestResolveCycleReportsPath(t *testing.T) {
	disc := []*Discovered{dep("a", "", "b"), dep("b", "", "a")}
	_, err := Resolve(disc, []string{"a"}, nil)
	if err == nil {
		t.Fatal("dependency cycle returned no error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q should identify a cycle", err)
	}
	// The path names both members and closes the loop (a → b → a).
	if !strings.Contains(err.Error(), "a → b → a") {
		t.Errorf("error %q should report the cycle path a → b → a", err)
	}
}

func TestResolveNoPlugins(t *testing.T) {
	got, err := Resolve(nil, nil, nil)
	if err != nil {
		t.Fatalf("empty Resolve errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty Resolve = %v, want empty", got)
	}
}
