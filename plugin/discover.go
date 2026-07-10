// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/inf"
)

// ParseFS parses a plugin from its own directory, as returned by a Source.Resolve.
// It selects the main .inf — the one named after the plugin if present, else the
// alphabetically first — reads it, and parses it for the given build. name is the
// plugin's identifier (its directory name).
func ParseFS(fsys fs.FS, name string, build int) (*Plugin, error) {
	main, ok := mainInfFS(fsys, name)
	if !ok {
		return nil, fmt.Errorf("plugin %q: no .inf found", name)
	}
	data, err := fs.ReadFile(fsys, main)
	if err != nil {
		return nil, err
	}
	f, err := inf.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", main, err)
	}
	return Parse(name, f, build)
}

// mainInfFS returns the main .inf within a plugin's fs.FS: the one named after the
// plugin if present, otherwise the alphabetically first. It returns ok=false if the
// directory holds no .inf.
func mainInfFS(fsys fs.FS, name string) (string, bool) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", false
	}
	var infs []string
	for _, e := range entries {
		if !e.IsDir() && isINF(e.Name()) {
			infs = append(infs, e.Name())
		}
	}
	if len(infs) == 0 {
		return "", false
	}
	sort.Strings(infs)
	preferred := name + ".inf"
	for _, n := range infs {
		if strings.EqualFold(n, preferred) {
			return n, true
		}
	}
	return infs[0], true
}

// ParseFile parses a single plugin .inf at path. The plugin name is the file's
// base name without extension.
func ParseFile(path string, build int) (*Plugin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := inf.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return Parse(name, f, build)
}

// Discover finds and parses the plugins in a drop-in directory. It follows the
// original tool's layout — one folder per plugin, the main .inf conventionally
// named after the folder — and also accepts bare .inf files placed directly in the
// directory. Results are returned in the alphabetical folder/name order the
// original processes plugins in (so a '!' prefix sorts first and 'z'/'zz' last).
//
// A plugin whose .inf fails to parse is reported in the returned error list but
// does not stop discovery of the rest.
func Discover(dir string, build int) ([]*Plugin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var paths []string // main .inf per plugin
	for _, e := range entries {
		if e.IsDir() {
			if main := mainInf(filepath.Join(dir, e.Name()), e.Name()); main != "" {
				paths = append(paths, main)
			}
			continue
		}
		if isINF(e.Name()) {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)

	var (
		plugins []*Plugin
		errs    []string
	)
	for _, p := range paths {
		pl, err := ParseFile(p, build)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		plugins = append(plugins, pl)
	}
	if len(errs) > 0 {
		return plugins, fmt.Errorf("%d plugin(s) failed to parse:\n%s", len(errs), strings.Join(errs, "\n"))
	}
	return plugins, nil
}

// isINF reports whether name has a .inf extension (case-insensitive).
func isINF(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".inf")
}

// mainInf returns the path to the main .inf of a plugin folder — the one named
// after the folder if present, otherwise the alphabetically first — or "" if the
// folder holds no .inf. It is the on-disk-path form of mainInfFS.
func mainInf(folder, name string) string {
	n, ok := mainInfFS(os.DirFS(folder), name)
	if !ok {
		return ""
	}
	return filepath.Join(folder, n)
}
