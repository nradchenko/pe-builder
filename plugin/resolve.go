// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"log/slog"
	"strings"
)

// Resolve expands the manifest-enabled set into the full ordered build set: the enabled
// plugins plus the transitive closure of their [Update] depends (drawn from discovered),
// ordered so every dependency precedes its dependents, with the load-order convention as
// the tie-break among otherwise-independent plugins. It folds the former Select, Order,
// and ValidateDepends into one dependency-aware pass.
//
// enabled names the manifest-enabled plugins (a name not discoverable is an error — a
// user typo). disabled names the plugins carrying an explicit `enabled = false` block; a
// required-but-disabled dependency is a conflict error, never a silent override. A
// depends target is matched case-insensitively against both plugin names and [Update] id
// values, and auto-enabling a dependency ignores its own [PEBuilder] Enable default —
// needed means enabled.
//
// Errors on: an enabled or depended-upon name that is not discoverable (unmet), a
// required dependency that is explicitly disabled (conflict), or a dependency cycle
// (reported as a readable path). With no depends edges the result is byte-for-byte
// today's Order over the enabled set (regression-guarded).
func Resolve(discovered []*Discovered, enabled, disabled []string) ([]*Discovered, error) {
	// Index the discovered set by name and by [Update] id (both lowercased). A name
	// wins over an id on collision, so a plugin is never shadowed by another's id.
	index := make(map[string]*Discovered, len(discovered)*2)
	for _, d := range discovered {
		index[strings.ToLower(d.Name)] = d
	}
	for _, d := range discovered {
		if id := d.Update().ID; id != "" {
			if _, taken := index[strings.ToLower(id)]; !taken {
				index[strings.ToLower(id)] = d
			}
		}
	}

	disabledSet := make(map[string]bool, len(disabled))
	for _, n := range disabled {
		disabledSet[strings.ToLower(n)] = true
	}

	// Seed the closure with the enabled plugins, in manifest order. An enabled name that
	// resolves to nothing is a user typo — collect them all and report once.
	var (
		order       []*Discovered // the closure, seeds first then deps in discovery order
		inSet       = map[string]bool{}
		missing     []string
		autoEnabled []autoEnable // deferred so a failed resolve logs nothing misleading
	)
	add := func(d *Discovered) bool {
		key := strings.ToLower(d.Name)
		if inSet[key] {
			return false
		}
		inSet[key] = true
		order = append(order, d)
		return true
	}
	for _, name := range enabled {
		d, ok := index[strings.ToLower(name)]
		if !ok {
			missing = append(missing, name)
			continue
		}
		add(d)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("enabled plugin(s) not found on the search path: %s", strings.Join(missing, ", "))
	}

	// Close over the transitive depends. order grows as dependencies are appended, so the
	// index-based walk covers the whole reachable set (diamonds de-dup, chains pull all).
	for i := 0; i < len(order); i++ {
		d := order[i]
		for _, dep := range d.Update().Depends {
			target, ok := index[strings.ToLower(dep)]
			if !ok {
				return nil, fmt.Errorf("plugin %q requires %q, which is not on the search path", d.Name, dep)
			}
			// A required plugin explicitly disabled under either of its identities (its
			// name or its [Update] id) is a conflict — depends is matched by name-or-id,
			// so the disable check must be too, or a disable-by-id would be overridden.
			if id := target.Update().ID; disabledSet[strings.ToLower(target.Name)] ||
				(id != "" && disabledSet[strings.ToLower(id)]) {
				return nil, fmt.Errorf("plugin %q requires %q, but %q is disabled", d.Name, dep, target.Name)
			}
			if add(target) {
				autoEnabled = append(autoEnabled, autoEnable{plugin: target.Name, requiredBy: d.Name})
			}
		}
	}

	result, err := topoOrder(order, index)
	if err != nil {
		return nil, err
	}
	// Resolve succeeded — now it is safe to report what the graph pulled in. Logging
	// during expansion would announce auto-enables for a build that then errors on a
	// cycle or an unmet transitive dependency.
	for _, a := range autoEnabled {
		slog.Info("auto-enabled dependency", "plugin", a.plugin, "required-by", a.requiredBy)
	}
	return result, nil
}

// autoEnable records a dependency pulled into the build by another plugin, for
// reporting once resolution has fully succeeded.
type autoEnable struct {
	plugin     string
	requiredBy string
}

// resNode is a plugin in the resolved closure with its dependency edges, used to
// topologically order the set.
type resNode struct {
	d          *Discovered
	idx        int        // position in discovery order (the stable final tie-break)
	deps       []*resNode // the plugins this one depends on (within the set)
	dependents []*resNode // the plugins that depend on this one
	indeg      int        // remaining unplaced dependencies
}

// topoOrder returns the closure ordered so every dependency precedes its dependents,
// breaking ties among independent plugins by the load-order convention (then discovery
// order, so a no-edge set reduces exactly to Order). It errors on a dependency cycle,
// reporting the cycle as a readable path.
func topoOrder(closure []*Discovered, index map[string]*Discovered) ([]*Discovered, error) {
	nodes := make([]*resNode, len(closure))
	byCanon := make(map[string]*resNode, len(closure))
	for i, d := range closure {
		n := &resNode{d: d, idx: i}
		nodes[i] = n
		byCanon[strings.ToLower(d.Name)] = n
	}
	for _, n := range nodes {
		seen := map[string]bool{}
		for _, dep := range n.d.Update().Depends {
			target := index[strings.ToLower(dep)] // in the closure by construction
			key := strings.ToLower(target.Name)
			if seen[key] {
				continue // depended twice (e.g. by name and by id) — one edge
			}
			seen[key] = true
			tn := byCanon[key]
			n.deps = append(n.deps, tn)
			tn.dependents = append(tn.dependents, n)
			n.indeg++
			// A dependency in a later load-order bucket must still load first; surface
			// that the naming convention was overridden by a real dependency.
			if loadBucket(tn.d.Name) > loadBucket(n.d.Name) {
				slog.Warn("dependency overrides load-order convention",
					"dependency", tn.d.Name, "placed-before", n.d.Name)
			}
		}
	}

	// Kahn's algorithm; among ready nodes pick the one that sorts first by the convention
	// (then discovery order). Sets are small, so a linear scan for the minimum is fine.
	var (
		result []*Discovered
		ready  []*resNode
		placed = make(map[*resNode]bool, len(nodes))
	)
	for _, n := range nodes {
		if n.indeg == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		best := 0
		for k := 1; k < len(ready); k++ {
			if readyLess(ready[k], ready[best]) {
				best = k
			}
		}
		n := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		result = append(result, n.d)
		placed[n] = true
		for _, dep := range n.dependents {
			dep.indeg--
			if dep.indeg == 0 {
				ready = append(ready, dep)
			}
		}
	}
	if len(result) < len(nodes) {
		return nil, fmt.Errorf("dependency cycle: %s", cyclePath(nodes, placed))
	}
	return result, nil
}

// readyLess orders two topologically-ready nodes: by the load-order convention, then by
// discovery order so equal-convention nodes keep their input order (matching Order's
// stable sort).
func readyLess(a, b *resNode) bool {
	if byConvention(a.d.Name, b.d.Name) {
		return true
	}
	if byConvention(b.d.Name, a.d.Name) {
		return false
	}
	return a.idx < b.idx
}

// cyclePath reconstructs a readable dependency cycle from the nodes Kahn's left unplaced.
// Every unplaced node still has an unplaced dependency, so following one such edge from
// any unplaced node necessarily revisits a node — closing the cycle (e.g. "a → b → a").
func cyclePath(nodes []*resNode, placed map[*resNode]bool) string {
	var start *resNode
	for _, n := range nodes {
		if !placed[n] {
			start = n
			break
		}
	}
	var (
		path   []*resNode
		onPath = map[*resNode]int{}
		cur    = start
	)
	for {
		if at, ok := onPath[cur]; ok {
			cycle := append(path[at:], cur)
			names := make([]string, len(cycle))
			for i, n := range cycle {
				names[i] = n.d.Name
			}
			return strings.Join(names, " → ")
		}
		onPath[cur] = len(path)
		path = append(path, cur)
		var next *resNode
		for _, dep := range cur.deps {
			if !placed[dep] {
				next = dep
				break
			}
		}
		cur = next
	}
}
