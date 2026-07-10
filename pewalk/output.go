// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// WriteText renders the closure as an indented dependency tree rooted at the
// seeds, followed by a summary. A module already expanded elsewhere in the tree
// is shown once and marked, so a cyclic or diamond graph stays finite. When
// missingOnly is true, the tree is replaced by a flat list of the unresolved
// modules (the red herrings).
func (r *Result) WriteText(w io.Writer, missingOnly bool) {
	if missingOnly {
		missing := r.Missing()
		if len(missing) == 0 {
			fmt.Fprintln(w, "No missing modules.")
		} else {
			for _, name := range missing {
				fmt.Fprintf(w, "%s\n", name)
			}
		}
		r.writeSummary(w)
		return
	}

	expanded := map[string]bool{}
	var walk func(name string, depth int)
	walk = func(name string, depth int) {
		node := r.Nodes[name]
		indent := strings.Repeat("  ", depth)
		fmt.Fprintf(w, "%s%s%s\n", indent, name, annotate(node))
		if node == nil || node.Status == StatusMissing || len(node.Imports) == 0 {
			return
		}
		if expanded[name] {
			if depth > 0 {
				// Already detailed above; avoid re-expanding shared subtrees.
				fmt.Fprintf(w, "%s  ...\n", indent)
			}
			return
		}
		expanded[name] = true
		for _, imp := range node.Imports {
			walk(imp, depth+1)
		}
	}
	for _, seed := range r.Seeds {
		walk(seed, 0)
	}
	r.writeSummary(w)
}

// annotate returns the bracketed status/origin suffix for a tree line.
func annotate(n *Node) string {
	if n == nil {
		return "  [MISSING]"
	}
	switch {
	case n.Status == StatusMissing:
		return "  [MISSING]"
	case n.ParseError != "":
		return fmt.Sprintf("  [%s — parse error: %s]", n.Origin, n.ParseError)
	default:
		return fmt.Sprintf("  [%s]", n.Origin)
	}
}

// writeSummary appends the closure's tally.
func (r *Result) writeSummary(w io.Writer) {
	resolved, missing, loose, comp, cab := r.Counts()
	fmt.Fprintf(w, "\nSummary: %d modules — %d resolved (%d loose, %d compressed, %d cabinet), %d missing\n",
		len(r.Order), resolved, loose, comp, cab, missing)
}

// WriteDOT renders the closure as a Graphviz digraph. Missing modules (red
// herrings) are dashed red, cabinet members blue, compressed-loose modules
// boxed, and seeds drawn with a heavier border. The graph of a full closure is
// dense — reduce it with `tred` or bound it with a max depth before laying it
// out.
func (r *Result) WriteDOT(w io.Writer) {
	fmt.Fprintln(w, "digraph pewalk {")
	fmt.Fprintln(w, "  rankdir=LR;")
	fmt.Fprintln(w, `  node [shape=ellipse, fontname="monospace"];`)

	for _, name := range r.Order {
		n := r.Nodes[name]
		if attrs := dotNodeAttrs(n); attrs != "" {
			fmt.Fprintf(w, "  %s [%s];\n", dotQuote(name), attrs)
		} else {
			fmt.Fprintf(w, "  %s;\n", dotQuote(name))
		}
	}
	for _, name := range r.Order {
		for _, imp := range r.Nodes[name].Imports {
			fmt.Fprintf(w, "  %s -> %s;\n", dotQuote(name), dotQuote(imp))
		}
	}
	fmt.Fprintln(w, "}")
}

// dotNodeAttrs builds the attribute list for a node, merging the styling for its
// status, origin, and seed role.
func dotNodeAttrs(n *Node) string {
	var attrs []string
	var styles []string
	if n.Status == StatusMissing {
		attrs = append(attrs, `color="red"`, `fontcolor="red"`)
		styles = append(styles, "dashed")
	} else {
		switch n.Origin.Kind {
		case OriginCab:
			attrs = append(attrs, `color="blue"`)
		case OriginCompressedLoose:
			attrs = append(attrs, `shape=box`)
		}
	}
	if n.Seed {
		attrs = append(attrs, "penwidth=2")
	}
	if len(styles) > 0 {
		attrs = append(attrs, fmt.Sprintf(`style="%s"`, strings.Join(styles, ",")))
	}
	return strings.Join(attrs, ", ")
}

// dotQuote returns name as a quoted, escaped DOT identifier.
func dotQuote(name string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(name) + `"`
}

// jsonOrigin is the wire form of an Origin.
type jsonOrigin struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

// jsonNode is the wire form of a Node.
type jsonNode struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Origin     jsonOrigin `json:"origin"`
	Imports    []string   `json:"imports"`
	Seed       bool       `json:"seed"`
	ParseError string     `json:"parse_error,omitempty"`
}

// jsonResult is the wire form of a Result.
type jsonResult struct {
	Seeds []string   `json:"seeds"`
	Nodes []jsonNode `json:"nodes"`
}

// originKindName maps an OriginKind to its JSON label.
func originKindName(k OriginKind) string {
	switch k {
	case OriginLoose:
		return "loose"
	case OriginCompressedLoose:
		return "compressed-loose"
	case OriginCab:
		return "cabinet"
	default:
		return "none"
	}
}

// WriteJSON renders the closure as machine-readable JSON, with nodes in discovery
// order for stable output.
func (r *Result) WriteJSON(w io.Writer) error {
	out := jsonResult{Seeds: r.Seeds}
	for _, name := range r.Order {
		n := r.Nodes[name]
		imports := n.Imports
		if imports == nil {
			imports = []string{}
		}
		out.Nodes = append(out.Nodes, jsonNode{
			Name:       n.Name,
			Status:     n.Status.String(),
			Origin:     jsonOrigin{Kind: originKindName(n.Origin.Kind), Path: n.Origin.Path},
			Imports:    imports,
			Seed:       n.Seed,
			ParseError: n.ParseError,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
