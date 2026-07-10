// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"reflect"
	"testing"
)

func TestFixtureImportsParse(t *testing.T) {
	pe := buildPE([]string{"GUI.dll", "leaf.dll"}, []string{"delaymod.dll"}, false)
	got, err := importsOf(pe)
	if err != nil {
		t.Fatalf("importsOf: %v", err)
	}
	want := []string{"delaymod.dll", "gui.dll", "leaf.dll"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("importsOf = %v, want %v", got, want)
	}
}
