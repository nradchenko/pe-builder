// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"reflect"
	"testing"
)

func TestFixtureImportsParse(t *testing.T) {
	pe := buildPE([]string{"GUI.dll", "leaf.dll"}, []string{"delaymod.dll"}, false)

	static, delay, err := importTables(pe)
	if err != nil {
		t.Fatalf("importTables: %v", err)
	}

	// With delay imports included, both tables merge into the union.
	got := mergeImports(static, delay, true)
	if want := []string{"delaymod.dll", "gui.dll", "leaf.dll"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeImports(includeDelay) = %v, want %v", got, want)
	}

	// Static-only drops the delay-load import.
	gotStatic := mergeImports(static, delay, false)
	if want := []string{"gui.dll", "leaf.dll"}; !reflect.DeepEqual(gotStatic, want) {
		t.Fatalf("mergeImports(static-only) = %v, want %v", gotStatic, want)
	}
}
