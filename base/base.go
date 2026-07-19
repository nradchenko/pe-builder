// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package base provides pebuild's built-in base layer: the core Win32 and shell
// file selection, registry seed, and PE boot edit that every build starts from,
// before any plugin. It is the clean-room equivalent of the original tool's base
// manifest.
//
// The base layer is a hybrid. Most of it is authored declaratively in an embedded
// INF (base.inf) and compiled to actions exactly like a plugin. The boot drivers,
// however, are derived from the source's own txtsetup.sif *.Load sections, because
// which drivers a minimal boot loads is a property of the source, not of pebuild.
package base

import (
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/plugin"
	"github.com/nradchenko/pe-builder/source"
)

//go:embed base.inf
var baseINF []byte

//go:embed startnet.cmd
var startnetCmd []byte

// StartnetCmd returns the built-in PE startup script placed at system32\startnet.cmd.
// winlogon runs it via SYSTEM\Setup\CmdLine once the environment reaches a shell;
// it is the environment's own file, not a copy from the Windows source.
func StartnetCmd() []byte { return startnetCmd }

// Actions returns the base layer's action list for a probed source. The file set is
// computed-plus-seeded: the import closure the scanners derive from the source (when
// pw is non-nil), the hand-curated seed and registry/boot edits in the embedded
// base.inf, and the boot-driver and locale copies derived from the source's own
// txtsetup.sif. The closure is placed first so the seed stays authoritative for any
// file it handles specially (a rename or a fixed directory). File actions are executed
// by the tree assembler; registry and boot actions are recorded for later stages.
//
// pw is the indexed source used to compute the import closure; passing nil yields the
// seed-only set (the closure adds no file the seed does not already justify), which
// keeps the base layer usable in tests that have no real source to index.
func Actions(pw *pewalk.Source, l *layout.Layout, id source.Identity) ([]action.Action, error) {
	var acts []action.Action
	if pw != nil {
		closure, redHerrings, err := ClosureActions(pw, l)
		if err != nil {
			return nil, fmt.Errorf("compute import closure: %w", err)
		}
		acts = append(acts, closure...)
		if len(redHerrings) > 0 {
			slog.Debug("import closure red herrings (absent from source, not copied)", "count", len(redHerrings))
		}
	}

	f, err := inf.Parse(baseINF)
	if err != nil {
		return nil, fmt.Errorf("parse base.inf: %w", err)
	}
	seed, err := plugin.Actions(f, plugin.TargetFor(id))
	if err != nil {
		return nil, fmt.Errorf("compile base.inf: %w", err)
	}
	acts = append(acts, seed...)
	acts = append(acts, driverActions(l)...)
	acts = append(acts, localeActions(l)...)
	return acts, nil
}
