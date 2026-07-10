// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"reflect"
	"testing"
)

// named builds a slice of Discovered plugins from names, in the given order.
func named(names ...string) []*Discovered {
	out := make([]*Discovered, len(names))
	for i, n := range names {
		out[i] = &Discovered{Name: n}
	}
	return out
}

func orderedNames(plugins []*Discovered) []string {
	out := make([]string, len(plugins))
	for i, p := range plugins {
		out[i] = p.Name
	}
	return out
}

func TestOrderConvention(t *testing.T) {
	// '!' first, 'z_'/'zz' last, the rest lexical in the middle — regardless of the
	// input (manifest) order.
	in := named("z_late", "mid", "!early", "alpha", "zz9", "Zebra")
	Order(in)
	// "Zebra" is a plain name (no z_/zz separator prefix match on "z_"; but "ze"
	// matches neither "z_" nor "zz"), so it sorts in the middle, case-insensitively.
	want := []string{"!early", "alpha", "mid", "Zebra", "z_late", "zz9"}
	if got := orderedNames(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Order = %v, want %v", got, want)
	}
}

func TestOrderPrefixCaseInsensitive(t *testing.T) {
	// The z_/zz last-group match folds case.
	in := named("beta", "Z_Custom", "ZZtop", "alpha")
	Order(in)
	want := []string{"alpha", "beta", "Z_Custom", "ZZtop"}
	if got := orderedNames(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Order = %v, want %v", got, want)
	}
}

func TestLoadBucket(t *testing.T) {
	cases := map[string]int{
		"!custom":      0,
		"alpha":        1,
		"zebra":        1, // 'ze' is neither 'z_' nor 'zz'
		"z_xpe-custom": 2,
		"zz5":          2,
		"ZZ_UPPER":     2,
	}
	for name, want := range cases {
		if got := loadBucket(name); got != want {
			t.Errorf("loadBucket(%q) = %d, want %d", name, got, want)
		}
	}
}
