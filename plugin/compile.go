// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/pewalk"
)

// Selected pairs a discovered plugin (in load order) with the manifest override layer
// applied to it and, for a build-capable plugin, the result of the [Build] pre-pass.
type Selected struct {
	Plugin   *Discovered
	Override Override
	// Built is non-nil when the plugin compiled its own payload; its produced files
	// resolve from the build output rather than the Windows source.
	Built *Built
}

// Compile flattens the selected plugins, in the given (load) order, into a single
// action list plus the file-origin map the tree assembler needs. It is the seam
// between plugin discovery and the build: the returned actions slot into the combined
// build action list after the base layer.
//
// For each plugin, in order: the [Closure] section's computed copies (the static import
// closure of its seed modules, read from the Windows source), then its own actions (a
// shipped copy tagged with the plugin name as Origin so its bytes are read from the
// plugin; a copy the manifest excludes dropped), then the override additions —
// include/inject copies and registry set/delete. The closure is placed first so the
// plugin's own [SourceDisksFiles] seed stays authoritative for any file it handles
// specially. resolveTarget returns a copy action's output-tree path; the caller supplies
// it (it needs the source layout) so files.exclude can match a plugin copy by its
// resolved destination.
//
// pw is the indexed source the [Closure] computation walks; passing nil skips closure
// computation (a plugin with no [Closure] is unaffected, and one with a [Closure] is
// then reduced to its explicit files — used by tests with no real source to index).
func Compile(selected []Selected, pw *pewalk.Source, resolveTarget func(action.Action) string) ([]action.Action, map[string]fs.FS, error) {
	var acts []action.Action
	origins := make(map[string]fs.FS)

	for _, s := range selected {
		d := s.Plugin
		if d.FS != nil {
			origins[d.Name] = d.FS
		}
		if s.Built != nil {
			origins[buildOrigin(d.Name)] = s.Built.Dir
		}

		// The [Closure] section's computed copies come from the Windows source (no
		// plugin Origin), placed before the plugin's own actions so its explicit seed
		// wins on conflict. Excludes and origin-stamping then apply to the whole set.
		ownActs := d.Actions()
		if seeds := d.ClosureSeeds(); len(seeds) > 0 && pw != nil {
			// A plugin's [Closure] seeds are files the plugin provides, so place them
			// too (emitSeeds), not just their dependencies.
			closureActs, redHerrings, err := ClosureActions(pw, seeds, true)
			if err != nil {
				return nil, nil, fmt.Errorf("plugin %q: compute [Closure]: %w", d.Name, err)
			}
			if len(redHerrings) > 0 {
				slog.Debug("plugin [Closure] red herrings (absent from source, not copied)",
					"plugin", d.Name, "count", len(redHerrings))
			}
			ownActs = append(closureActs, ownActs...)
		}

		own := applyExcludes(ownActs, d, s.Override.Exclude, s.Built, resolveTarget)
		acts = append(acts, own...)

		ovActs, ovOrigins, err := overrideAdditions(s.Override, d.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("plugin %q: %w", d.Name, err)
		}
		acts = append(acts, ovActs...)
		for k, v := range ovOrigins {
			origins[k] = v
		}
	}
	return acts, origins, nil
}

// applyExcludes returns acts (a plugin's own actions plus any computed [Closure] copies)
// with its copies origin-stamped and the copies whose resolved output path matches a
// files.exclude entry dropped. A copy of a file the plugin's [Build] produced is served
// from the build output; a copy of a file the plugin ships is served from the plugin
// directory; a [Closure]-computed copy carries neither stamp and is read from the
// Windows source. A non-matching exclude is warned about. resolveTarget maps a copy to
// its output-tree path (needs the source layout, so the caller supplies it); it is only
// consulted when the plugin has excludes.
func applyExcludes(acts []action.Action, d *Discovered, exclude []string, built *Built, resolveTarget func(action.Action) string) []action.Action {
	want := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		want[strings.ToLower(normalizeTreePath(e))] = true
	}
	matched := make(map[string]bool, len(want))

	var out []action.Action
	for _, a := range acts {
		if a.Kind == action.CopyFile {
			if len(want) > 0 {
				if key := strings.ToLower(normalizeTreePath(resolveTarget(a))); want[key] {
					matched[key] = true
					continue // suppressed by files.exclude
				}
			}
			switch {
			case built != nil && built.Produces[strings.ToLower(a.Src)]:
				a.Origin = buildOrigin(d.Name)
			case a.ShipWithPlugin:
				a.Origin = d.Name
			}
		}
		out = append(out, a)
	}
	for e := range want {
		if !matched[e] {
			slog.Warn("plugin files.exclude matched no file", "plugin", d.Name, "path", e)
		}
	}
	return out
}

// overrideAdditions builds the actions a plugin's override layer adds (include,
// inject, registry set/delete) and the origins its injected local files read from.
func overrideAdditions(ov Override, plugin string) ([]action.Action, map[string]fs.FS, error) {
	if ov.empty() {
		return nil, nil, nil
	}
	var acts []action.Action
	origins := make(map[string]fs.FS)

	for _, inc := range ov.Include {
		acts = append(acts, includeAction(inc))
	}
	for i, inj := range ov.Inject {
		a, dir, err := injectAction(inj, fmt.Sprintf("inject:%s:%d", plugin, i))
		if err != nil {
			return nil, nil, err
		}
		origins[a.Origin] = os.DirFS(dir)
		acts = append(acts, a)
	}

	setActs, err := regSetActions(ov.RegSet)
	if err != nil {
		return nil, nil, err
	}
	acts = append(acts, setActs...)

	delActs, err := regDeleteActions(ov.RegDelete)
	if err != nil {
		return nil, nil, err
	}
	acts = append(acts, delActs...)

	return acts, origins, nil
}
