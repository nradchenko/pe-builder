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
// Do not take this from source.Identity: its Flavor is looked up from the build number alone,
// so XP Professional x64 Edition — build 3790 and amd64, exactly like Server 2003 x64 —
// probes as "win2003", and anything identity-derived turns it into a server. txtsetup.sif is
// the one place the two disagree.
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
