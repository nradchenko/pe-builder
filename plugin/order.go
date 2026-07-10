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
		return byConvention(plugins[i].Name, plugins[j].Name)
	})
}

// byConvention reports whether plugin name a sorts before b under the load-order
// convention: the '!'/'z_' bucket first, then ascending case-insensitive name. It is
// the shared comparator behind both Order and Resolve's topological tie-break — with no
// dependency edges, Resolve reduces exactly to Order.
func byConvention(a, b string) bool {
	ba, bb := loadBucket(a), loadBucket(b)
	if ba != bb {
		return ba < bb
	}
	return strings.ToLower(a) < strings.ToLower(b)
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
