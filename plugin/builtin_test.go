// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"io/fs"
	"testing"

	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/plugins"
)

// TestBuiltinBuildCapablePluginsShipSource guards the plugins/embed.go embedding. Every
// built-in plugin's .inf is embedded by a `*/*.inf` glob, but a build-capable plugin's
// source (its Makefile and the files `make` compiles) is embedded by an explicit
// per-plugin line. If that line is forgotten, only the .inf is embedded and the [Build]
// pre-pass fails at image-build time — RunBuild copies the embedded directory and runs the
// build command (default "make") in it. This asserts every embedded plugin that declares a
// [Build] section ships its build source, so the omission is caught here instead.
func TestBuiltinBuildCapablePluginsShipSource(t *testing.T) {
	const build = 2600 // section filtering only; any real build number works

	dirs, err := fs.ReadDir(plugins.FS, ".")
	if err != nil {
		t.Fatalf("read embedded plugins: %v", err)
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		name := dir.Name()
		// Skip a directory that is not a plugin, by the same rule discovery uses — it
		// holds no .inf. plugins/.shared is one: it carries build-support files the engine
		// materializes into a plugin's build directory, not a plugin of its own.
		if !isPluginDir(plugins.FS, name) {
			continue
		}
		sub, err := fs.Sub(plugins.FS, name)
		if err != nil {
			t.Fatalf("sub %q: %v", name, err)
		}
		p, err := ParseFS(sub, name, Target{Build: build})
		if err != nil {
			t.Fatalf("parse built-in %q: %v", name, err)
		}
		if !p.Build.Present {
			continue // declarative plugin: the glob-embedded .inf is all it needs
		}
		// A [Build] plugin is built by materializing its embedded dir and running its
		// command (default "make"), so its Makefile and at least one source file must be
		// embedded alongside the .inf.
		if _, err := fs.Stat(sub, "Makefile"); err != nil {
			t.Errorf("built-in %q declares [Build] but ships no Makefile; add its source to "+
				"plugins/embed.go (the */*.inf glob embeds only the .inf)", name)
		}
		if !hasBuildSource(t, sub) {
			t.Errorf("built-in %q declares [Build] but ships no source file beyond its .inf and "+
				"Makefile; add its source to plugins/embed.go", name)
		}
	}
}

// TestBuiltinsDeclareNoEnable keeps [PEBuilder] Enable out of the plugins we ship.
//
// pebuild parses the key for the format's sake and acts on none of it: a build enables what
// its manifest names, plus the depends closure of those. A built-in carrying Enable=1 would
// state something untrue — poweroff carried one and was never enabled by default — and even
// Enable=0 is a line that looks like it decides something. Our .inf files are what people
// copy when writing their own, so the key stays out of them entirely. A foreign plugin may
// declare whatever it likes; `plugins info` tells its author the declaration is inert.
//
// Checked on the raw section rather than through Plugin.DeclaredEnable, which cannot tell
// an absent key from Enable=0 — the distinction this test exists to hold.
func TestBuiltinsDeclareNoEnable(t *testing.T) {
	dirs, err := fs.ReadDir(plugins.FS, ".")
	if err != nil {
		t.Fatalf("read embedded plugins: %v", err)
	}
	for _, dir := range dirs {
		if !dir.IsDir() || !isPluginDir(plugins.FS, dir.Name()) {
			continue
		}
		name := dir.Name()
		sub, err := fs.Sub(plugins.FS, name)
		if err != nil {
			t.Fatalf("sub %q: %v", name, err)
		}
		entries, err := fs.ReadDir(sub, ".")
		if err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
		for _, e := range entries {
			if e.IsDir() || !isINF(e.Name()) {
				continue
			}
			b, err := fs.ReadFile(sub, e.Name())
			if err != nil {
				t.Fatalf("read %s/%s: %v", name, e.Name(), err)
			}
			f, err := inf.Parse(b)
			if err != nil {
				t.Fatalf("parse %s/%s: %v", name, e.Name(), err)
			}
			if _, ok := sectionMap(f.Section("PEBuilder", 0))["enable"]; ok {
				t.Errorf("%s/%s declares [PEBuilder] Enable, which nothing reads; drop the line "+
					"— the manifest is what enables a plugin", name, e.Name())
			}
		}
	}
}

// hasBuildSource reports whether the plugin directory embeds a build source file — anything
// that is not the plugin .inf or the Makefile (e.g. a .c the build compiles).
func hasBuildSource(t *testing.T, sub fs.FS) bool {
	t.Helper()
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read plugin dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); !isINF(n) && n != "Makefile" {
			return true
		}
	}
	return false
}
