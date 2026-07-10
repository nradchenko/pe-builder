// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// discoverAll runs Discover over the discovery fixture tree and returns the result
// keyed by plugin name for convenient assertions.
func discoverAll(t *testing.T) (map[string]*Discovered, []*Discovered) {
	t.Helper()
	sp := SearchPath{DirSource{Root: "testdata/discover", Label: "test"}}
	disc, err := Discover(sp, 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*Discovered, len(disc))
	for _, d := range disc {
		byName[d.Name] = d
	}
	return byName, disc
}

func TestDiscoverNamesAndOrder(t *testing.T) {
	_, disc := discoverAll(t)
	var names []string
	for _, d := range disc {
		names = append(names, d.Name)
	}
	// sp.List sorts by name; the non-manifest "data" directory is skipped.
	want := []string{"!early", "alpha", "bundle", "z_late"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("discovered names = %v, want %v", names, want)
	}
}

func TestDiscoverProvenanceAndMetadata(t *testing.T) {
	byName, _ := discoverAll(t)

	alpha := byName["alpha"]
	if alpha == nil {
		t.Fatal("alpha not discovered")
	}
	if alpha.Source != "test" {
		t.Errorf("alpha source = %q, want test", alpha.Source)
	}
	if !alpha.Enabled() {
		t.Error("alpha should be enabled by default (Enable=1)")
	}

	late := byName["z_late"]
	if late.Enabled() {
		t.Error("z_late should be disabled by default (Enable=0)")
	}
	if late.Update().Parent != "alpha" {
		t.Errorf("z_late parent = %q, want alpha", late.Update().Parent)
	}
}

func TestDiscoverBundleUnits(t *testing.T) {
	byName, _ := discoverAll(t)
	b := byName["bundle"]
	if b == nil {
		t.Fatal("bundle not discovered")
	}

	// Two manifests parsed (bundle.inf main first, then extra.inf); support.inf,
	// which lacks [PEBuilder], is not a unit.
	var units []string
	for _, u := range b.Units {
		units = append(units, u.Name)
	}
	if want := []string{"bundle", "extra"}; !reflect.DeepEqual(units, want) {
		t.Errorf("bundle units = %v, want %v", units, want)
	}

	// Actions aggregate across units, main first.
	acts := b.Actions()
	if len(acts) != 2 {
		t.Fatalf("bundle actions = %d, want 2 (one AddReg per unit)", len(acts))
	}
	if acts[0].ValueName != "Main" || acts[1].ValueName != "Extra" {
		t.Errorf("bundle action order = [%s %s], want [Main Extra]", acts[0].ValueName, acts[1].ValueName)
	}
}

func TestSelect(t *testing.T) {
	byName, disc := discoverAll(t)
	_ = byName

	// Selection preserves the given (manifest) order and matches case-insensitively.
	sel, err := Select(disc, []string{"z_late", "ALPHA"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range sel {
		got = append(got, d.Name)
	}
	if want := []string{"z_late", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Select order = %v, want %v (given order preserved, case-folded)", got, want)
	}
}

func TestParseFSSkipsSupportManifest(t *testing.T) {
	// The .inf named after the plugin is a shipped support file (no [PEBuilder]); the
	// real manifest is another .inf. ParseFS shares parseUnits with Discover, so it
	// skips the support file and returns the real manifest rather than erroring.
	manifest := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"Real\"\nEnable=1\n"
	support := "[Version]\nSignature=\"$Windows NT$\"\n[Strings]\nx=\"y\"\n"
	fsys := fstest.MapFS{
		"foo.inf":  &fstest.MapFile{Data: []byte(support)},  // name-matching support file
		"real.inf": &fstest.MapFile{Data: []byte(manifest)}, // the actual manifest
	}
	p, err := ParseFS(fsys, "foo", 0)
	if err != nil {
		t.Fatalf("ParseFS errored on a support main .inf: %v", err)
	}
	if p.Display != "Real" {
		t.Errorf("ParseFS returned %q, want the real manifest (Display=Real)", p.Display)
	}

	// A directory with .inf files but no plugin manifest is a clear error.
	noManifest := fstest.MapFS{"foo.inf": &fstest.MapFile{Data: []byte(support)}}
	if _, err := ParseFS(noManifest, "foo", 0); err == nil {
		t.Error("ParseFS on a manifest-less directory returned no error")
	}
}

func TestSelectMissingErrors(t *testing.T) {
	_, disc := discoverAll(t)
	_, err := Select(disc, []string{"alpha", "ghost", "phantom"})
	if err == nil {
		t.Fatal("Select with missing plugins returned no error")
	}
	// The error names every unmatched plugin.
	for _, name := range []string{"ghost", "phantom"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name missing plugin %q", err, name)
		}
	}
}
