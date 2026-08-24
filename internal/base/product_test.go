// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"testing"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/layout"
)

func productType(t *testing.T, l *layout.Layout) string {
	t.Helper()
	acts := productActions(l)
	if len(acts) != 1 {
		t.Fatalf("got %d actions, want 1", len(acts))
	}
	a := acts[0]
	if a.Kind != action.AddReg || a.Hive != action.HiveSystem {
		t.Errorf("got kind %v hive %v, want AddReg on the system hive", a.Kind, a.Hive)
	}
	if a.Subkey != `ControlSet001\Control\ProductOptions` || a.ValueName != "ProductType" {
		t.Errorf("got %s\\%s, want ControlSet001\\Control\\ProductOptions\\ProductType",
			a.Subkey, a.ValueName)
	}
	return a.Data
}

func TestProductActions(t *testing.T) {
	if got := productType(t, &layout.Layout{ServerProduct: true}); got != productTypeServer {
		t.Errorf("server source: ProductType = %q, want %q", got, productTypeServer)
	}
	if got := productType(t, &layout.Layout{ServerProduct: false}); got != productTypeWorkstation {
		t.Errorf("workstation source: ProductType = %q, want %q", got, productTypeWorkstation)
	}
	// A layout that never read the value at all still has to yield something valid.
	if got := productType(t, nil); got != productTypeWorkstation {
		t.Errorf("nil layout: ProductType = %q, want %q", got, productTypeWorkstation)
	}
}

// TestProductTypeComesFromTheMedia pins the *input* rather than the mapping: productActions
// must read the layout and nothing else, so no later edit can reintroduce an Identity-based
// guess — which would make XP x64 a server, since it probes identically to Server 2003 x64.
// Parsing the value itself is layout's job and is tested there.
func TestProductTypeComesFromTheMedia(t *testing.T) {
	// Same build number and architecture as a server source would have; only the media's
	// own answer differs, and it is the only thing productActions is allowed to consult.
	xpX64 := &layout.Layout{ServerProduct: false}
	server := &layout.Layout{ServerProduct: true}

	if got := productType(t, xpX64); got != productTypeWorkstation {
		t.Errorf("XP x64 must stay a workstation, got %q", got)
	}
	if got := productType(t, server); got != productTypeServer {
		t.Errorf("Server 2003 must be a server, got %q", got)
	}
}
