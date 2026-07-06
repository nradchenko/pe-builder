// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
)

// driverActions turns the source's declared boot drivers into copy actions. The
// driver set comes from the source's txtsetup.sif *.Load sections (HAL, disk,
// filesystem, bus, input, keyboard, display), so it adapts to the source rather than
// being hardcoded. Each driver's target directory is resolved from the source layout
// by the tree assembler, so no directory is set here.
//
// The actions are marked optional: a source lists more boot drivers (many exotic SCSI
// adapters) than it ships loose in I386 — the rest live only inside DRIVER.CAB, whose
// member extraction is future work. Marking them optional copies the drivers present
// loose and records the cabinet-only ones as skipped instead of failing the build.
func driverActions(l *layout.Layout) []action.Action {
	drivers := l.BootDrivers()
	out := make([]action.Action, 0, len(drivers))
	for _, name := range drivers {
		out = append(out, action.Action{
			Kind:     action.CopyFile,
			Src:      name,
			Optional: true,
		})
	}
	return out
}
