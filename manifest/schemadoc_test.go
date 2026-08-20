// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// cataloguePath is the field catalogue: docs/manifest.hcl, a valid manifest that shows
// every block and field with each optional one at its default.
const cataloguePath = "../docs/manifest.hcl"

// TestCatalogueDocumentsEverySchemaField fails when a field the schema accepts is named
// nowhere in the catalogue. It closes the half of the drift problem that decoding cannot
// reach: TestShippedManifestsAreValid catches a field *removed or renamed* in the schema,
// because the catalogue then fails to decode, but a field *added* to the schema leaves
// every existing manifest valid, so nothing notices that it went undocumented.
//
// The limit is worth stating: this proves a field is *named*, not that what the catalogue
// or docs/manifest.md says about it is true. Only reading them checks that.
func TestCatalogueDocumentsEverySchemaField(t *testing.T) {
	want := schemaNames(reflect.TypeOf(rawManifest{}), "")
	got := catalogueNames(t)

	var missing []string
	for _, n := range want {
		if !got[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	for _, n := range missing {
		t.Errorf("the schema accepts %q, but the catalogue never mentions it — document it in %s", n, cataloguePath)
	}
}

// schemaNames walks the raw decoding structs and returns every name the HCL schema
// accepts, each qualified by the blocks containing it ("output.volume_label",
// "plugin.files.include"). It reads the hcl struct tags directly rather than going
// through gohcl.ImpliedBodySchema, which reports one body at a time and so cannot
// produce the nesting these qualified names need.
func schemaNames(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag, ok := t.Field(i).Tag.Lookup("hcl")
		if !ok {
			continue
		}
		name, kind, _ := strings.Cut(tag, ",")

		switch kind {
		case "label":
			// The block's label (plugin "luna"), not a field of its own: it cannot be
			// written without writing the block, so the block's entry covers it.
			continue
		case "remain":
			// rawOptions.Remain captures a plugin's own free-form [Options] settings.
			// Those are declared by each plugin, not by this schema, so there is no
			// fixed set for the catalogue to carry.
			continue
		}

		qualified := name
		if prefix != "" {
			qualified = prefix + "." + name
		}
		out = append(out, qualified)

		if kind == "block" {
			out = append(out, schemaNames(t.Field(i).Type, qualified)...)
			continue
		}
		// An attribute whose Go type is a struct decodes as an object rather than a
		// scalar, and its keys are named by cty tags instead of hcl ones — `inject` is
		// the one such field today. Those keys are written in the catalogue
		// ({ from = ..., to = ... }); they are simply not HCL attributes, so they are
		// collected from the object's own keys at both ends rather than skipped.
		out = append(out, ctyNames(t.Field(i).Type, qualified)...)
	}
	return out
}

// ctyNames returns the object keys of an attribute that decodes into a struct, read
// from its cty tags. It does not recurse: nothing in this schema nests objects.
func ctyNames(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if name, ok := t.Field(i).Tag.Lookup("cty"); ok {
			out = append(out, prefix+"."+name)
		}
	}
	return out
}

// catalogueNames parses the catalogue and returns every block and attribute name it
// writes, qualified the same way. It walks the parsed AST rather than a decoded
// manifest: a field set to its zero value (keep_work = false) is indistinguishable from
// an absent one after decoding, and a plain text search would pass on a name that only
// ever appears inside a comment.
func catalogueNames(t *testing.T) map[string]bool {
	t.Helper()

	f, diags := hclparse.NewParser().ParseHCLFile(cataloguePath)
	if diags.HasErrors() {
		t.Fatalf("parse %s: %v", cataloguePath, diags)
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s is not native HCL syntax, so its AST cannot be walked", cataloguePath)
	}

	out := make(map[string]bool)
	var walk func(b *hclsyntax.Body, prefix string)
	walk = func(b *hclsyntax.Body, prefix string) {
		qualify := func(name string) string {
			if prefix == "" {
				return name
			}
			return prefix + "." + name
		}
		for name, attr := range b.Attributes {
			q := qualify(name)
			out[q] = true
			// An attribute holding objects carries its own names — inject's from/to.
			// They are keys inside the expression, not attributes of the body, so the
			// body walk alone would leave them uncovered.
			for _, key := range objectKeys(attr.Expr) {
				out[q+"."+key] = true
			}
		}
		for _, blk := range b.Blocks {
			q := qualify(blk.Type)
			out[q] = true
			// Repeated blocks of one type are unioned: the catalogue spreads the plugin
			// override layers over several plugin blocks (options on one, files and
			// registry on another) rather than cramming them into a single unrealistic one.
			walk(blk.Body, q)
		}
	}
	walk(body, "")
	return out
}

// objectKeys returns the keys of any object the expression writes, looking through a
// list of them. Only bare identifier keys count — ExprAsKeyword yields "" for anything
// else — which is exactly the form a documented field takes.
func objectKeys(e hclsyntax.Expression) []string {
	var out []string
	switch v := e.(type) {
	case *hclsyntax.TupleConsExpr:
		for _, el := range v.Exprs {
			out = append(out, objectKeys(el)...)
		}
	case *hclsyntax.ObjectConsExpr:
		for _, item := range v.Items {
			if kw := hcl.ExprAsKeyword(item.KeyExpr); kw != "" {
				out = append(out, kw)
			}
		}
	}
	return out
}
