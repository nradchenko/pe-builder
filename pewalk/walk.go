// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

// Status is a module's resolution outcome in the closure.
type Status int

const (
	// StatusResolved means the module was found in the source and parsed.
	StatusResolved Status = iota
	// StatusMissing means the module is imported but absent from the source — a
	// flagged red herring, reported but never fatal.
	StatusMissing
)

// String renders a status as a short label.
func (s Status) String() string {
	if s == StatusMissing {
		return "missing"
	}
	return "resolved"
}

// Node is one module in the closure, keyed in Result by its canonical name.
type Node struct {
	// Name is the bare, lowercased module name (the canonical key).
	Name string
	// Status is the resolution outcome.
	Status Status
	// Origin records where a resolved module was read from (zero when Missing).
	Origin Origin
	// Imports are the module's imported module names (sorted), empty when Missing
	// or when the module has no imports.
	Imports []string
	// Seed is true when the module was given as a walk seed.
	Seed bool
	// ParseError, when non-empty, records that the module resolved but its PE
	// could not be parsed for imports; it is otherwise treated as resolved.
	ParseError string
}

// Result is the computed import closure: every reachable module, the seeds it
// grew from, and a deterministic discovery order.
type Result struct {
	// Seeds are the canonical names of the walk's starting modules.
	Seeds []string
	// Nodes maps a canonical module name to its node.
	Nodes map[string]*Node
	// Order lists every node's name in breadth-first discovery order.
	Order []string
}

// Options tunes a walk.
type Options struct {
	// MaxDepth bounds recursion depth from the seeds (0 = unbounded). Seeds are at
	// depth 0; a module imported by a seed is at depth 1.
	MaxDepth int
}

// Walk computes the transitive import closure of seeds against s, following both
// static and delay-load imports. It never fails on a missing module: an imported
// module absent from the source becomes a Missing node. Modules are de-duplicated
// by canonical name.
func Walk(s *Source, seeds ...string) (*Result, error) {
	return WalkWith(s, Options{}, seeds...)
}

// WalkWith is Walk with explicit options.
func WalkWith(s *Source, opts Options, seeds ...string) (*Result, error) {
	r := &Result{Nodes: map[string]*Node{}}

	type queued struct {
		name  string
		depth int
	}
	var queue []queued
	enqueue := func(name string, depth int, seed bool) {
		key := moduleName(name)
		if key == "" {
			return
		}
		if n, ok := r.Nodes[key]; ok {
			if seed {
				n.Seed = true
			}
			return
		}
		n := &Node{Name: key, Seed: seed}
		r.Nodes[key] = n
		r.Order = append(r.Order, key)
		queue = append(queue, queued{name: key, depth: depth})
	}

	for _, seed := range seeds {
		key := moduleName(seed)
		if key == "" {
			continue
		}
		r.Seeds = append(r.Seeds, key)
		enqueue(seed, 0, true)
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		node := r.Nodes[item.name]

		data, origin, err := s.Read(item.name)
		if err != nil {
			node.Status = StatusMissing
			continue
		}
		node.Status = StatusResolved
		node.Origin = origin

		// At the depth cap a module is a leaf: its imports are neither recorded
		// nor enqueued, which keeps every edge pointing at a discovered node.
		if opts.MaxDepth > 0 && item.depth >= opts.MaxDepth {
			continue
		}

		imports, err := importsOf(data)
		if err != nil {
			node.ParseError = err.Error()
			continue
		}
		node.Imports = imports

		for _, imp := range imports {
			enqueue(imp, item.depth+1, false)
		}
	}

	return r, nil
}

// Missing returns the canonical names of every unresolved module, in discovery
// order.
func (r *Result) Missing() []string {
	var out []string
	for _, name := range r.Order {
		if r.Nodes[name].Status == StatusMissing {
			out = append(out, name)
		}
	}
	return out
}

// Counts tallies the closure by resolution outcome and origin.
func (r *Result) Counts() (resolved, missing, loose, compressedLoose, cab int) {
	for _, name := range r.Order {
		n := r.Nodes[name]
		if n.Status == StatusMissing {
			missing++
			continue
		}
		resolved++
		switch n.Origin.Kind {
		case OriginLoose:
			loose++
		case OriginCompressedLoose:
			compressedLoose++
		case OriginCab:
			cab++
		}
	}
	return
}
