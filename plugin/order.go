// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"sort"
	"strings"
)

// Order sorts plugins into load order in place, following the original tool's
// convention so that later plugins override earlier ones:
//
//   - a '!' prefix sorts first (e.g. "!custom"),
//   - a 'z_' or 'zz' prefix sorts last (e.g. "z_xpe-custom", "zz5"),
//   - everything else sorts in the middle,
//
// and within each of those three groups plugins are ordered by ascending
// case-insensitive name. The sort is stable, so where the convention leaves two
// plugins in the same group with the same name the input order (the manifest's
// declaration order) is preserved as the tie-break.
func Order(plugins []*Discovered) {
	sort.SliceStable(plugins, func(i, j int) bool {
		bi, bj := loadBucket(plugins[i].Name), loadBucket(plugins[j].Name)
		if bi != bj {
			return bi < bj
		}
		return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
	})
}

// loadBucket ranks a plugin name into the three load-order groups: 0 for a '!'
// prefix (first), 2 for a 'z_'/'zz' prefix (last, matched case-insensitively), 1
// otherwise.
func loadBucket(name string) int {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(name, "!"):
		return 0
	case strings.HasPrefix(lower, "z_"), strings.HasPrefix(lower, "zz"):
		return 2
	default:
		return 1
	}
}
