// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// Discovered is a plugin found on the search path: its canonical name (the on-disk
// directory name), the provenance of the source it came from, and its parsed INF
// unit(s). A plugin is a directory; a directory may hold more than one plugin-manifest
// .inf (a bundle, like XPE), in which case every manifest is a unit — the one named
// after the directory first, the rest in name order. Non-manifest .inf files in the
// directory (shipped support files without a [PEBuilder] section) are not units.
type Discovered struct {
	// Name is the canonical directory name.
	Name string
	// Source is the provenance label of the resolving source (e.g. "project").
	Source string
	// FS is the plugin's directory, from which its shipped files are read when the
	// build places a [SourceDisksFiles] entry the plugin ships itself.
	FS fs.FS
	// Units are the parsed plugin manifests, main first.
	Units []*Plugin
}

// Main returns the primary unit — the manifest named after the directory, or the
// first in name order — whose [PEBuilder]/[Update] metadata represents the plugin.
func (d *Discovered) Main() *Plugin {
	if len(d.Units) == 0 {
		return nil
	}
	return d.Units[0]
}

// Enabled reports the plugin's default enabled state, taken from the main unit's
// [PEBuilder] Enable. The manifest's plugin block overrides this at selection time.
func (d *Discovered) Enabled() bool {
	m := d.Main()
	return m != nil && m.Enabled
}

// Update returns the main unit's [Update] section (id/depends/parent).
func (d *Discovered) Update() Update {
	m := d.Main()
	if m == nil {
		return Update{}
	}
	return m.Update
}

// Build returns the main unit's [Build] section; its Present field reports whether
// the plugin compiles its own payload from source at build time.
func (d *Discovered) Build() BuildSpec {
	m := d.Main()
	if m == nil {
		return BuildSpec{}
	}
	return m.Build
}

// Actions returns every unit's compiled actions concatenated in unit order — the
// plugin's full contribution to the build.
func (d *Discovered) Actions() []action.Action {
	var out []action.Action
	for _, u := range d.Units {
		out = append(out, u.Actions...)
	}
	return out
}

// ClosureSeeds returns every unit's [Closure] seed modules concatenated in unit order.
// Their static import closure is computed into the plugin's file set at build time.
func (d *Discovered) ClosureSeeds() []string {
	var out []string
	for _, u := range d.Units {
		out = append(out, u.Closure...)
	}
	return out
}

// Discover scans the search path and returns every available plugin, de-duplicated
// by name with search-path precedence (a more specific source shadows a less
// specific one), in name order. t selects the conditional section variants (see Parse).
//
// A directory that holds .inf files but no plugin manifest (no [PEBuilder] section)
// is not a plugin and is skipped. A manifest that fails to parse is collected into
// the returned error; the plugins that parsed cleanly are still returned, so one
// broken plugin does not hide the rest.
func Discover(sp SearchPath, t Target) ([]*Discovered, error) {
	var (
		out  []*Discovered
		errs []string
	)
	for _, a := range sp.List() {
		fsys, canonical, ok := a.Source.Resolve(a.Name)
		if !ok {
			// Listed by the source but no longer resolvable (a race); skip it.
			continue
		}
		units, err := parseUnits(fsys, canonical, t)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if len(units) == 0 {
			// .inf(s) present but none is a plugin manifest — not a plugin.
			continue
		}
		out = append(out, &Discovered{Name: canonical, Source: a.Source.Name(), FS: fsys, Units: units})
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("%d plugin(s) failed to parse:\n%s", len(errs), strings.Join(errs, "\n"))
	}
	return out, nil
}

// parseUnits parses every plugin-manifest .inf in a plugin's directory into a unit,
// the manifest named after the directory first and the rest in name order. A .inf
// without a [PEBuilder] section is a shipped support file, not a manifest, and is
// skipped; a malformed manifest is an error.
func parseUnits(fsys fs.FS, name string, t Target) ([]*Plugin, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var infs []string
	for _, e := range entries {
		if !e.IsDir() && isINF(e.Name()) {
			infs = append(infs, e.Name())
		}
	}
	sort.Strings(infs)

	var units []*Plugin
	for _, n := range mainFirst(infs, name) {
		data, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		f, err := inf.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: parse %s: %w", name, n, err)
		}
		if !f.HasSection("PEBuilder") {
			// A shipped support .inf, not a plugin manifest.
			continue
		}
		unitName := strings.TrimSuffix(n, path.Ext(n))
		p, err := Parse(unitName, f, t)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", name, err)
		}
		units = append(units, p)
	}
	return units, nil
}

// mainFirst reorders infs so the .inf named after the plugin (matched
// case-insensitively) is first, keeping the remaining names in their existing order.
// If none matches the plugin name, infs is returned unchanged, so the alphabetically
// first .inf leads.
func mainFirst(infs []string, name string) []string {
	preferred := name + ".inf"
	for i, n := range infs {
		if strings.EqualFold(n, preferred) {
			ordered := make([]string, 0, len(infs))
			ordered = append(ordered, n)
			ordered = append(ordered, infs[:i]...)
			ordered = append(ordered, infs[i+1:]...)
			return ordered
		}
	}
	return infs
}

// ParseFS parses a single plugin from its own directory, as returned by
// Source.Resolve, and returns its main manifest unit. name is the plugin's canonical
// directory name. It shares parseUnits with Discover, so it selects the main .inf and
// skips shipped support .inf files identically; a directory with no plugin manifest
// is an error. Used by `plugins info`.
func ParseFS(fsys fs.FS, name string, t Target) (*Plugin, error) {
	units, err := parseUnits(fsys, name, t)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("plugin %q: no plugin manifest (.inf with a [PEBuilder] section) found", name)
	}
	return units[0], nil
}

// isINF reports whether name has a .inf extension (case-insensitive).
func isINF(name string) bool {
	return strings.EqualFold(path.Ext(name), ".inf")
}
