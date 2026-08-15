// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// EnvVar names extra plugin directories (path-list separated) added to the search
// path after the project dir and before the global dir.
const EnvVar = "PEBUILD_PLUGIN_PATH"

// SearchPath is an ordered list of plugin sources. Earlier sources take precedence:
// on Resolve the first match wins, and on List a name offered by an earlier source
// shadows the same name in a later one. The precedence order (most specific first)
// is project dir, PEBUILD_PLUGIN_PATH, global dir, built-ins.
type SearchPath []Source

// Available is a plugin offered by the search path and the source it resolved from.
type Available struct {
	Name   string
	Source Source
}

// Resolve returns the directory of the first source that offers name, that source
// (for provenance), the plugin's canonical on-disk name, and whether any did.
func (sp SearchPath) Resolve(name string) (fs.FS, Source, string, bool) {
	for _, s := range sp {
		if fsys, canonical, ok := s.Resolve(name); ok {
			return fsys, s, canonical, true
		}
	}
	return nil, nil, "", false
}

// List returns every available plugin across the path, de-duplicated by name with
// the first (most specific) source winning, sorted by name.
func (sp SearchPath) List() []Available {
	seen := make(map[string]Available)
	for _, s := range sp {
		names, err := s.List()
		if err != nil {
			// A broken source (e.g. an unreadable dir) does not sink the rest.
			continue
		}
		for _, n := range names {
			key := strings.ToLower(n)
			if _, dup := seen[key]; !dup {
				seen[key] = Available{Name: n, Source: s}
			}
		}
	}
	out := make([]Available, 0, len(seen))
	for _, a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// LocalPluginsDir is the conventional project-local plugins directory (cwd-relative)
// the example manifests use as plugins_dir. The interactive plugin-management commands
// include it in their search path (see ManagementSearchPath); a build does not — a
// build resolves plugins only from its declared inputs (the manifest plugins_dir or an
// explicit dir), so its output does not silently depend on the current directory, and
// a plugin's [Build] command is never picked up implicitly.
const LocalPluginsDir = "plugins"

// LocalSourceLabel is the search-path label LocalPluginsDir is added under, and is what
// a caller matches on to tell that tier apart — the CLI warns that a build will not
// resolve from it. Exported so that relabelling the tier moves both, rather than leaving
// a caller comparing against a literal that no longer occurs.
const LocalSourceLabel = "local"

// DefaultSearchPath assembles the plugin search path a build resolves from, in
// precedence order: the project plugins_dir (projectDir, if set), the
// PEBUILD_PLUGIN_PATH entries, the per-user global dir, and the embedded built-ins.
// It deliberately does not include the local ./plugins dir — see ManagementSearchPath.
//
// Explicitly named directories — projectDir and each PEBUILD_PLUGIN_PATH entry —
// must exist and be readable; a missing or unreadable one is a clear error, since
// naming it is direct user intent. The global dir and built-ins are implicit and
// stay lenient: a missing global dir is simply skipped.
func DefaultSearchPath(projectDir string) (SearchPath, error) {
	return searchPath(projectDir, false)
}

// ManagementSearchPath is DefaultSearchPath plus a lenient local ./plugins tier
// (skipped if absent), placed just below an explicit project dir. It is for the
// interactive `plugins list`/`info` commands, so a plugin installed with
// `plugins add --local` is visible; it is not used for builds.
func ManagementSearchPath(projectDir string) (SearchPath, error) {
	return searchPath(projectDir, true)
}

func searchPath(projectDir string, includeLocal bool) (SearchPath, error) {
	var sp SearchPath
	if projectDir != "" {
		if err := validateDir(projectDir); err != nil {
			return nil, fmt.Errorf("plugins dir %q: %w", projectDir, err)
		}
		sp = append(sp, DirSource{Root: projectDir, Label: "project"})
	}
	if includeLocal && projectDir != LocalPluginsDir {
		// Lenient: a missing ./plugins is simply not a source (unlike an explicit dir).
		sp = append(sp, DirSource{Root: LocalPluginsDir, Label: LocalSourceLabel})
	}
	for _, d := range envDirs() {
		if err := validateDir(d); err != nil {
			return nil, fmt.Errorf("%s entry %q: %w", EnvVar, d, err)
		}
		sp = append(sp, DirSource{Root: d, Label: "env"})
	}
	if g := GlobalDir(); g != "" {
		sp = append(sp, DirSource{Root: g, Label: "global"})
	}
	sp = append(sp, Builtin())
	return sp, nil
}

// validateDir reports whether path is a readable directory. It returns a distinct
// error for a path that does not exist, is not a directory, or cannot be read —
// used to fail fast on an explicitly named plugin dir.
func validateDir(path string) error {
	// os.ReadDir surfaces all three failures — missing (fs.ErrNotExist), not a
	// directory, and permission denied — in one call.
	if _, err := os.ReadDir(path); err != nil {
		return err
	}
	return nil
}

// envDirs returns the directories named by PEBUILD_PLUGIN_PATH, in order.
func envDirs() []string {
	v := os.Getenv(EnvVar)
	if v == "" {
		return nil
	}
	var out []string
	for _, d := range filepath.SplitList(v) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// GlobalDir returns the per-user global plugins directory,
// <data>/pe-builder/plugins, where <data> is $XDG_DATA_HOME when set, else the
// platform's per-user data directory. It returns "" only if the home directory
// cannot be determined.
func GlobalDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		switch runtime.GOOS {
		case "windows":
			if base = os.Getenv("APPDATA"); base == "" {
				base = filepath.Join(home, "AppData", "Roaming")
			}
		case "darwin":
			base = filepath.Join(home, "Library", "Application Support")
		default:
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, "pe-builder", "plugins")
}
