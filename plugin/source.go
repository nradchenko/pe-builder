// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Source is a provider of plugins: a place plugins are looked up by name. A plugin
// is a directory holding one or more .inf files (a bundle is several .inf files in
// one directory); Resolve returns that directory as an fs.FS so an embedded and an
// on-disk plugin are read the same way.
//
// Implementations now are EmbeddedSource (the built-ins, go:embed) and DirSource (a
// drop-in directory). The interface is the seam for later, additive sources — a
// pinned git clone (reusing DirSource) or a fetch-by-name registry — without a
// redesign.
type Source interface {
	// Name is a short provenance label shown by `plugins list` (e.g. "built-in").
	Name() string
	// List returns the plugin names this source offers, sorted.
	List() ([]string, error)
	// Resolve returns the plugin's directory as an fs.FS, its canonical name (the
	// on-disk directory name, which may differ in case from the requested name),
	// and whether it was found. Names are matched case-insensitively.
	Resolve(name string) (fs.FS, string, bool)
}

// DirSource is a Source backed by a drop-in directory whose immediate
// subdirectories are plugins. A missing directory is treated as empty, so an
// unconfigured global or project dir is skipped rather than an error.
type DirSource struct {
	// Root is the filesystem path holding plugin subdirectories.
	Root string
	// Label overrides the provenance shown by `plugins list`; Root is used if empty.
	Label string
}

// Name returns the source's provenance label (Label, or Root when unset).
func (s DirSource) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return s.Root
}

// List returns the plugin names in the directory, sorted.
func (s DirSource) List() ([]string, error) { return fsList(os.DirFS(s.Root)) }

// Resolve returns the named plugin's directory as an fs.FS and its canonical name.
func (s DirSource) Resolve(name string) (fs.FS, string, bool) {
	return fsResolve(os.DirFS(s.Root), name)
}

// EmbeddedSource is a Source backed by an fs.FS (typically a go:embed of the
// built-in plugins) whose top-level directories are plugins.
type EmbeddedSource struct {
	// Label is the provenance shown by `plugins list`.
	Label string
	// FS holds plugin directories at its root.
	FS fs.FS
}

// Name returns the source's provenance label.
func (s EmbeddedSource) Name() string { return s.Label }

// List returns the plugin names in the embedded FS, sorted.
func (s EmbeddedSource) List() ([]string, error) { return fsList(s.FS) }

// Resolve returns the named plugin's directory as an fs.FS and its canonical name.
func (s EmbeddedSource) Resolve(name string) (fs.FS, string, bool) {
	return fsResolve(s.FS, name)
}

// fsList returns the names of the plugin directories at the root of fsys — the
// immediate subdirectories that contain at least one .inf — sorted. A root that
// does not exist yields no plugins and no error, so an absent drop-in dir is simply
// skipped.
func fsList(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && isPluginDir(fsys, e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// fsResolve returns the directory of the plugin named name (matched
// case-insensitively) as an fs.FS rooted at that directory, along with the
// canonical on-disk name of the matched directory.
func fsResolve(fsys fs.FS, name string) (fs.FS, string, bool) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, "", false
	}
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), name) && isPluginDir(fsys, e.Name()) {
			sub, err := fs.Sub(fsys, e.Name())
			if err != nil {
				return nil, "", false
			}
			return sub, e.Name(), true
		}
	}
	return nil, "", false
}

// isPluginDir reports whether dir within fsys holds at least one .inf — the
// definition of a plugin directory.
func isPluginDir(fsys fs.FS, dir string) bool {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && isINF(e.Name()) {
			return true
		}
	}
	return false
}
