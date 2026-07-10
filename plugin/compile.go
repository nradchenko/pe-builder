// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"io/fs"

	"github.com/nradchenko/pe-builder/action"
)

// Compile flattens the selected plugins, in the given (load) order, into a single
// action list plus the file-origin map the tree assembler needs. It is the seam
// between plugin discovery and the build: the returned actions slot into the
// combined build action list after the base layer.
//
// A copy action for a file the plugin ships itself (the ShipWithPlugin attribute) is
// tagged with the plugin's name as its Origin, and origins[name] is the plugin's
// directory, so those bytes are read from the plugin. A copy of a file the plugin
// merely names from the Windows source (no ShipWithPlugin) keeps an empty Origin and
// resolves from the source — including its driver cabinets. Non-copy actions
// (registry, text edits) pass through unchanged; they carry no origin.
func Compile(selected []*Discovered) ([]action.Action, map[string]fs.FS) {
	var acts []action.Action
	origins := make(map[string]fs.FS)
	for _, d := range selected {
		if d.FS != nil {
			origins[d.Name] = d.FS
		}
		for _, a := range d.Actions() {
			if a.Kind == action.CopyFile && a.ShipWithPlugin {
				a.Origin = d.Name
			}
			acts = append(acts, a)
		}
	}
	return acts, origins
}
