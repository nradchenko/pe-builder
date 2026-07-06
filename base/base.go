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

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/plugin"
	"github.com/nradchenko/pe-builder/source"
)

//go:embed base.inf
var baseINF []byte

// Actions returns the base layer's action list for a probed source: the embedded
// base.inf actions (core Win32 + shell files, registry seed, boot edit) followed by
// the boot-driver copy actions derived from the source's *.Load sections. File
// actions are executed by the tree assembler; registry and boot actions are recorded
// for later stages.
func Actions(l *layout.Layout, id source.Identity) ([]action.Action, error) {
	f, err := inf.Parse(baseINF)
	if err != nil {
		return nil, fmt.Errorf("parse base.inf: %w", err)
	}
	acts, err := plugin.Actions(f, id.Build)
	if err != nil {
		return nil, fmt.Errorf("compile base.inf: %w", err)
	}
	return append(acts, driverActions(l)...), nil
}
