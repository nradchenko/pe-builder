// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/layout"
)

// NT product types, as the value ProductOptions carries.
const (
	productTypeWorkstation = "WinNT"
	productTypeServer      = "ServerNT"
)

// productActions derives the NT product type from the source's own txtsetup.sif, the way the
// boot drivers and the locale are derived.
//
// It cannot come from source.Identity, which carries only what the media state about
// themselves as a whole: XP Professional x64 Edition is build 3790 on amd64, exactly like
// Server 2003 x64, so anything derived from build and architecture makes one of them the
// other. The media's own setup metadata is where the two finally disagree.
func productActions(l *layout.Layout) []action.Action {
	t := productTypeWorkstation
	if l != nil && l.ServerProduct {
		t = productTypeServer
	}
	return []action.Action{{
		Kind:      action.AddReg,
		Hive:      action.HiveSystem,
		Subkey:    `ControlSet001\Control\ProductOptions`,
		ValueName: "ProductType",
		RegType:   action.RegSZ,
		Data:      t,
		HasValue:  true,
	}}
}
