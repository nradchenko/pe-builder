// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"testing"
	"testing/fstest"
)

// discoverAll runs Discover over the discovery fixture tree and returns the result
// keyed by plugin name for convenient assertions.
func discoverAll(t *testing.T) (map[string]*Discovered, []*Discovered) {
	t.Helper()
	sp := SearchPath{DirSource{Root: "testdata/discover", Label: "test"}}
	disc, err := Discover(sp, Target{})
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*Discovered, len(disc))
	for _, d := range disc {
		byName[d.Name] = d
	}
	return byName, disc
}

// TestDiscoverKeepsArchUnbuildablePlugin pins the altitude of the missing-[Build.<arch>]
// rule. A plugin that scopes its build by architecture but declares no variant for the
// target cannot be BUILT for it — but it must still be discovered, because Discover drops
// any plugin whose manifest fails to parse (discover.go: parse error -> continue). If the
// rule were enforced at parse time, such a plugin would vanish from the search path: absent
// from `plugins list`, and a manifest enabling it would report "plugin not found" rather
// than naming the real problem. The refusal belongs to RunBuild, which is the only
// operation it actually breaks (see TestRunBuildRejectsMissingArchVariant).
func TestDiscoverKeepsArchUnbuildablePlugin(t *testing.T) {
	const manifest = `[Version]
Signature="$Windows NT$"
[PEBuilder]
Name="i386 only"
[Build]
Produces = drv.sys
[Build.i386]
Requires = i686-w64-mingw32-gcc
Command  = make ARCH=i386
`
	sp := SearchPath{EmbeddedSource{FS: fstest.MapFS{
		"i386only/i386only.inf": {Data: []byte(manifest)},
	}, Label: "test"}}

	disc, err := Discover(sp, Target{Arch: "amd64"})
	if err != nil {
		t.Fatalf("discovery reported an error for a plugin that only lacks an amd64 build: %v", err)
	}
	if len(disc) != 1 {
		t.Fatalf("discovered %d plugins, want 1 — an i386-only plugin must stay visible on amd64", len(disc))
	}
	if got := disc[0].Build().MissingArchVariant; got != "amd64" {
		t.Errorf("MissingArchVariant = %q, want amd64", got)
	}
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
	// Enable= is parsed but never acted on (see Plugin.DeclaredEnable). Pinned here
	// because `pebuild plugins info` reads the parsed value to tell the author of a
	// plugin declaring Enable=1 that it does nothing: misparse it and that note either
	// goes missing or appears on a plugin that never declared it.
	if !alpha.Main().DeclaredEnable {
		t.Error("alpha: DeclaredEnable = false, want true (its .inf declares Enable=1)")
	}

	late := byName["z_late"]
	if late.Main().DeclaredEnable {
		t.Error("z_late: DeclaredEnable = true, want false (its .inf declares Enable=0)")
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
	p, err := ParseFS(fsys, "foo", Target{})
	if err != nil {
		t.Fatalf("ParseFS errored on a support main .inf: %v", err)
	}
	if p.Display != "Real" {
		t.Errorf("ParseFS returned %q, want the real manifest (Display=Real)", p.Display)
	}

	// A directory with .inf files but no plugin manifest is a clear error.
	noManifest := fstest.MapFS{"foo.inf": &fstest.MapFile{Data: []byte(support)}}
	if _, err := ParseFS(noManifest, "foo", Target{}); err == nil {
		t.Error("ParseFS on a manifest-less directory returned no error")
	}
}
