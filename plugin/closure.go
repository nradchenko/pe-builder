// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/pewalk"
)

// ClosureActions computes copy actions for the transitive static import closure of
// seeds against the indexed source pw: one CopyFile per module the walk discovers.
// Modules absent from the source are red herrings under the source-self-sufficiency
// principle — anything truly needed is on the media — so they are returned separately
// for reporting, never copied.
//
// emitSeeds decides whether the seed roots themselves are placed. The base layer passes
// false: its roots (kernel, the boot EXEs, the boot drivers) are placed by the base
// manifest and driver derivation, which own their renames and fixed directories, so the
// closure emits only their dependencies. A plugin's [Closure] passes true: its seeds
// (shell32, comctl32, …) ARE the files the plugin provides, so it must place them too,
// not just their dependencies.
//
// The walk follows static imports only: a module's static imports load with it and must
// be present, while its delay imports load lazily and mostly never do in a minimal
// environment, so following them transitively over-includes. Each emitted copy is
// optional, so a resolved-but-unplaceable module is recorded as skipped rather than
// failing the build.
//
// This is the shared engine behind both the base layer's computed file set and a
// plugin's [Closure] section, so a plugin declares a small seed of GUI/runtime roots
// and pebuild computes the shared dependency set from the source's own import graph.
func ClosureActions(pw *pewalk.Source, seeds []string, emitSeeds bool) (acts []action.Action, redHerrings []string, err error) {
	res, err := pewalk.WalkWith(pw, pewalk.Options{StaticOnly: true}, seeds...)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range res.Order {
		n := res.Nodes[name]
		switch {
		case n.Status == pewalk.StatusMissing:
			redHerrings = append(redHerrings, name)
		case n.Seed && !emitSeeds:
			// A base root placed by its owner (the base manifest or driver derivation),
			// which controls its rename and target directory — not by the closure.
		default:
			acts = append(acts, action.Action{Kind: action.CopyFile, Src: name, Optional: true})
		}
	}
	return acts, redHerrings, nil
}

// parseClosure reads the optional [Closure] section: the pewalk seed modules whose
// static import closure the engine computes into the plugin's file set. Entries are one
// bare module name per line (e.g. shell32.dll); a key=value form is tolerated by taking
// the key. It is purely additive — a plugin with no [Closure] computes no closure and
// uses its [SourceDisksFiles] exactly as before.
func parseClosure(f *inf.File, build int) []string {
	var out []string
	for _, l := range f.Section("Closure", build) {
		seed := l.Field(0)
		if seed == "" {
			seed = l.Key
		}
		if seed = strings.TrimSpace(seed); seed != "" {
			out = append(out, seed)
		}
	}
	return out
}
