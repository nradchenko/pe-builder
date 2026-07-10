// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"reflect"
	"testing"
)

func TestFixtureImportsParse(t *testing.T) {
	pe := buildPE([]string{"GUI.dll", "leaf.dll"}, []string{"delaymod.dll"}, false)

	// With delay imports included, both tables are returned.
	got, err := importsOf(pe, true)
	if err != nil {
		t.Fatalf("importsOf: %v", err)
	}
	if want := []string{"delaymod.dll", "gui.dll", "leaf.dll"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("importsOf(includeDelay) = %v, want %v", got, want)
	}

	// Static-only drops the delay-load import.
	gotStatic, err := importsOf(pe, false)
	if err != nil {
		t.Fatalf("importsOf static: %v", err)
	}
	if want := []string{"gui.dll", "leaf.dll"}; !reflect.DeepEqual(gotStatic, want) {
		t.Fatalf("importsOf(static-only) = %v, want %v", gotStatic, want)
	}
}
