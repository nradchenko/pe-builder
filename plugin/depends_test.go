// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"strings"
	"testing"
)

// dep builds a Discovered whose main unit carries an [Update] with the given id and
// depends list.
func dep(name, id string, depends ...string) *Discovered {
	return &Discovered{
		Name:  name,
		Units: []*Plugin{{Name: name, Update: Update{Present: true, ID: id, Depends: depends}}},
	}
}

func TestValidateDependsSatisfied(t *testing.T) {
	sel := []*Discovered{dep("base", "base"), dep("tool", "tool", "base")}
	if err := ValidateDepends(sel); err != nil {
		t.Errorf("satisfied depends errored: %v", err)
	}
}

func TestValidateDependsByID(t *testing.T) {
	// A depends target may name another plugin's [Update] id, not just its folder name.
	sel := []*Discovered{dep("core-plugin", "core"), dep("tool", "tool", "core")}
	if err := ValidateDepends(sel); err != nil {
		t.Errorf("depends-by-id errored: %v", err)
	}
}

func TestValidateDependsUnmet(t *testing.T) {
	sel := []*Discovered{dep("tool", "tool", "ramdisk")}
	err := ValidateDepends(sel)
	if err == nil {
		t.Fatal("unmet dependency returned no error")
	}
	if !strings.Contains(err.Error(), "tool") || !strings.Contains(err.Error(), "ramdisk") {
		t.Errorf("error %q should name both the dependent and the missing dependency", err)
	}
}

func TestValidateDependsNone(t *testing.T) {
	if err := ValidateDepends([]*Discovered{dep("solo", "solo")}); err != nil {
		t.Errorf("no depends errored: %v", err)
	}
}
