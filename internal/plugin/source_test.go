// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

// embedded builds an EmbeddedSource whose plugins are the named directories, each
// given a minimal valid .inf so it counts as a plugin directory.
func embedded(label string, names ...string) EmbeddedSource {
	m := fstest.MapFS{}
	for _, n := range names {
		m[n+"/"+n+".inf"] = &fstest.MapFile{
			Data: []byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"" + n + "\"\n"),
		}
	}
	return EmbeddedSource{Label: label, FS: m}
}

func TestSourceListAndResolve(t *testing.T) {
	s := embedded("built-in", "alpha", "beta")

	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(names, want) {
		t.Errorf("List() = %v, want %v", names, want)
	}

	// Resolve is case-insensitive, returns the plugin's own directory, and reports
	// the canonical on-disk name rather than the requested casing.
	fsys, canonical, ok := s.Resolve("ALPHA")
	if !ok {
		t.Fatal("Resolve(ALPHA) not found")
	}
	if canonical != "alpha" {
		t.Errorf("Resolve(ALPHA) canonical = %q, want alpha", canonical)
	}
	if _, err := fsys.Open("alpha.inf"); err != nil {
		t.Errorf("resolved fs missing alpha.inf: %v", err)
	}
	if _, _, ok := s.Resolve("missing"); ok {
		t.Error("Resolve(missing) reported found")
	}
}

func TestNonPluginDirIgnored(t *testing.T) {
	// A directory without an .inf is not a plugin.
	m := fstest.MapFS{
		"real/real.inf":        &fstest.MapFile{Data: []byte("x")},
		"notplugin/readme.txt": &fstest.MapFile{Data: []byte("x")},
	}
	s := EmbeddedSource{Label: "e", FS: m}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"real"}; !reflect.DeepEqual(names, want) {
		t.Errorf("List() = %v, want %v", names, want)
	}
	if _, _, ok := s.Resolve("notplugin"); ok {
		t.Error("Resolve(notplugin) reported found")
	}
}

func TestDirSourceMissingRootIsEmpty(t *testing.T) {
	s := DirSource{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	names, err := s.List()
	if err != nil {
		t.Fatalf("List() on missing dir errored: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("List() = %v, want empty", names)
	}
	if _, _, ok := s.Resolve("anything"); ok {
		t.Error("Resolve on missing dir reported found")
	}
}

func TestDirSourceReadsTestdata(t *testing.T) {
	s := DirSource{Root: "testdata/drop"}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"!first", "sample", "z_last"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("List() = %v, want %v", names, want)
	}
	if _, _, ok := s.Resolve("sample"); !ok {
		t.Error("Resolve(sample) not found")
	}
}

func TestSearchPathPrecedence(t *testing.T) {
	// project shadows global shadows built-in for the same name.
	project := embedded("project", "shared", "proj-only")
	global := embedded("global", "shared", "glob-only")
	builtin := embedded("built-in", "shared", "builtin-only")
	sp := SearchPath{project, global, builtin}

	// Resolve: the most specific source wins.
	_, src, _, ok := sp.Resolve("shared")
	if !ok || src.Name() != "project" {
		t.Errorf("Resolve(shared) source = %v (ok=%v), want project", src, ok)
	}
	_, src, _, _ = sp.Resolve("glob-only")
	if src.Name() != "global" {
		t.Errorf("Resolve(glob-only) source = %v, want global", src.Name())
	}
	_, src, _, _ = sp.Resolve("builtin-only")
	if src.Name() != "built-in" {
		t.Errorf("Resolve(builtin-only) source = %v, want built-in", src.Name())
	}

	// List: de-duplicated, first source wins, sorted, provenance labeled.
	got := map[string]string{}
	var order []string
	for _, a := range sp.List() {
		got[a.Name] = a.Source.Name()
		order = append(order, a.Name)
	}
	if got["shared"] != "project" {
		t.Errorf("List shared source = %q, want project", got["shared"])
	}
	wantOrder := []string{"builtin-only", "glob-only", "proj-only", "shared"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("List order = %v, want %v", order, wantOrder)
	}
}

func TestEnvDirs(t *testing.T) {
	t.Setenv(EnvVar, string(filepath.ListSeparator)+"one"+string(filepath.ListSeparator)+"two"+string(filepath.ListSeparator))
	got := envDirs()
	if want := []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("envDirs() = %v, want %v (empty entries dropped)", got, want)
	}

	t.Setenv(EnvVar, "")
	if got := envDirs(); got != nil {
		t.Errorf("envDirs() with empty var = %v, want nil", got)
	}
}

func TestDefaultSearchPathOrder(t *testing.T) {
	proj, env := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(EnvVar, env)
	sp, err := DefaultSearchPath(proj)
	if err != nil {
		t.Fatal(err)
	}

	var labels []string
	for _, s := range sp {
		labels = append(labels, s.Name())
	}
	want := []string{"project", "env", "global", BuiltinLabel}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("search-path labels = %v, want %v", labels, want)
	}
}

func TestDefaultSearchPathNoProject(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(EnvVar, "")
	sp, err := DefaultSearchPath("")
	if err != nil {
		t.Fatal(err)
	}

	var labels []string
	for _, s := range sp {
		labels = append(labels, s.Name())
	}
	if want := []string{"global", BuiltinLabel}; !reflect.DeepEqual(labels, want) {
		t.Errorf("search-path labels = %v, want %v", labels, want)
	}
}

func TestDefaultSearchPathExplicitDirErrors(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// A missing project dir is an explicit-intent error, not silently skipped.
	t.Setenv(EnvVar, "")
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := DefaultSearchPath(missing); err == nil {
		t.Error("DefaultSearchPath with a missing --dir returned no error")
	}

	// A missing PEBUILD_PLUGIN_PATH entry likewise errors.
	t.Setenv(EnvVar, missing)
	if _, err := DefaultSearchPath(""); err == nil {
		t.Errorf("DefaultSearchPath with a missing %s entry returned no error", EnvVar)
	}
}

func TestManagementSearchPathLocalTier(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(EnvVar, "")

	// A build's path excludes the local ./plugins tier; the management path includes it.
	build, err := DefaultSearchPath("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range build {
		if s.Name() == "local" {
			t.Error("DefaultSearchPath (build) must not include the local ./plugins tier")
		}
	}

	mgmt, err := ManagementSearchPath("")
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, s := range mgmt {
		labels = append(labels, s.Name())
	}
	if want := []string{LocalSourceLabel, "global", BuiltinLabel}; !reflect.DeepEqual(labels, want) {
		t.Errorf("management path = %v, want %v", labels, want)
	}
}

func TestGlobalDirXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data/home")
	if got, want := GlobalDir(), filepath.Join("/data/home", "pe-builder", "plugins"); got != want {
		t.Errorf("GlobalDir() = %q, want %q", got, want)
	}
}

func TestParseFS(t *testing.T) {
	s := DirSource{Root: "testdata/drop"}
	fsys, canonical, ok := s.Resolve("SAMPLE")
	if !ok {
		t.Fatal("Resolve(SAMPLE) not found")
	}
	if canonical != "sample" {
		t.Fatalf("canonical = %q, want sample", canonical)
	}
	// Parsing under the canonical name gives the on-disk identity regardless of the
	// requested casing.
	p, err := ParseFS(fsys, canonical, Target{Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "sample" {
		t.Errorf("Name = %q, want sample", p.Name)
	}
	if p.Update.ID != "sample" {
		t.Errorf("Update.ID = %q, want sample", p.Update.ID)
	}
	if len(p.Actions) == 0 {
		t.Error("expected compiled actions")
	}
}
