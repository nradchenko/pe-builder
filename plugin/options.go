// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// OptionType constrains what a plugin option's value (a declared default or a manifest
// override) may be. It is the value's type, checked when an override is resolved.
type OptionType string

const (
	// OptionString accepts any text; it is the default when a declaration omits the type.
	OptionString OptionType = "string"
	// OptionInt accepts a setupapi integer (decimal, or 0x-prefixed hex).
	OptionInt OptionType = "int"
	// OptionBool accepts a boolean written true/false or 1/0 (case-insensitive).
	OptionBool OptionType = "bool"
)

// valid reports whether t is a known option type.
func (t OptionType) valid() bool {
	switch t {
	case OptionString, OptionInt, OptionBool:
		return true
	}
	return false
}

// Option is one entry of a plugin's [Options] section: a manifest-settable knob the
// plugin substitutes into its INF values. The plugin declares the name, a default, and
// optionally a type and an enumerated set of allowed values; the manifest overrides the
// value in a plugin block's options {} block. Substitution is one-to-one text: an option
// replaces a $(name) placeholder in an INF value with its (override or default) value.
type Option struct {
	// Name is the option's identifier, matched case-insensitively against a $(name)
	// placeholder and a manifest override key.
	Name string
	// Default is the value used when the manifest does not override the option.
	Default string
	// Type constrains the value; empty declarations default to OptionString.
	Type OptionType
	// Allowed, when non-empty, is the enumerated set of values the option accepts (in
	// addition to the type check). For an int option the comparison is numeric.
	Allowed []string
}

// validate reports whether value satisfies the option's type and allowed set.
func (o Option) validate(value string) error {
	switch o.Type {
	case OptionInt:
		if _, ok := inf.ParseInt(value); !ok {
			return fmt.Errorf("value %q is not an integer", value)
		}
	case OptionBool:
		if !isBool(value) {
			return fmt.Errorf("value %q is not a boolean (want true/false)", value)
		}
	}
	if len(o.Allowed) > 0 && !o.allows(value) {
		return fmt.Errorf("value %q is not one of the allowed values [%s]", value, strings.Join(o.Allowed, ", "))
	}
	return nil
}

// allows reports whether value matches one of the option's allowed entries. An int
// option compares numerically (so 0x20 and 32 match); any other type compares the text
// case-insensitively.
func (o Option) allows(value string) bool {
	if o.Type == OptionInt {
		if n, ok := inf.ParseInt(value); ok {
			for _, a := range o.Allowed {
				if m, ok := inf.ParseInt(a); ok && m == n {
					return true
				}
			}
			return false
		}
	}
	for _, a := range o.Allowed {
		if strings.EqualFold(a, value) {
			return true
		}
	}
	return false
}

// isBool reports whether s is a recognized boolean literal.
func isBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "false", "1", "0":
		return true
	}
	return false
}

// parseOptions reads the optional [Options] section: name = default[, type][, allowed...].
// The default is field 0; an optional type is field 1 (empty means string); any further
// fields are the enumerated allowed set. A declared default is itself validated against
// the option's type/allowed, so a malformed declaration is a plugin error caught at parse
// time rather than a surprise at substitution.
func parseOptions(f *inf.File, build int) ([]Option, error) {
	if !f.HasSection("Options") {
		return nil, nil
	}
	var out []Option
	seen := make(map[string]bool)
	for _, l := range f.Section("Options", build) {
		name := strings.TrimSpace(l.Key)
		if name == "" {
			return nil, fmt.Errorf("[Options]: line %q has no option name", l.Raw())
		}
		low := strings.ToLower(name)
		if seen[low] {
			return nil, fmt.Errorf("[Options]: duplicate option %q", name)
		}
		seen[low] = true

		o := Option{Name: name, Default: l.Field(0), Type: OptionString}
		if t := strings.TrimSpace(l.Field(1)); t != "" {
			ot := OptionType(strings.ToLower(t))
			if !ot.valid() {
				return nil, fmt.Errorf("[Options]: option %q has unknown type %q (want string, int, or bool)", name, t)
			}
			o.Type = ot
		}
		if fields := l.Fields(); len(fields) > 2 {
			o.Allowed = append([]string(nil), fields[2:]...)
		}
		if err := o.validate(o.Default); err != nil {
			return nil, fmt.Errorf("[Options]: option %q default: %w", name, err)
		}
		out = append(out, o)
	}
	return out, nil
}

// declaredOptions aggregates the [Options] of every unit of a discovered plugin into one
// set, in unit order. Options belong to the plugin as a whole, so a name declared by two
// units is a conflict rather than a silent last-wins.
func declaredOptions(d *Discovered) ([]Option, error) {
	var out []Option
	seen := make(map[string]bool)
	for _, u := range d.Units {
		for _, o := range u.Options {
			low := strings.ToLower(o.Name)
			if seen[low] {
				return nil, fmt.Errorf("option %q declared by more than one unit", o.Name)
			}
			seen[low] = true
			out = append(out, o)
		}
	}
	return out, nil
}

// EffectiveOptions returns the option values a selected plugin is built with: its
// declared defaults overlaid with the manifest's overrides. Compile substitutes exactly
// these into the plugin's INF values, so a caller that needs to *report* what a build
// used (the build receipt) reads them from here rather than recomputing the overlay and
// risking a different answer.
func EffectiveOptions(s Selected) (map[string]string, error) {
	declared, err := declaredOptions(s.Plugin)
	if err != nil {
		return nil, err
	}
	return ResolveOptions(declared, s.Override.Options)
}

// ResolveOptions overlays a manifest's option overrides on a plugin's declared option
// defaults, validating each override against its declaration, and returns the effective
// value of every declared option keyed by its declared name. An override of an option the
// plugin does not declare, or a value that fails the option's type/allowed check, is a
// clear error. Errors are reported in a deterministic (sorted) order.
func ResolveOptions(declared []Option, overrides map[string]string) (map[string]string, error) {
	byName := make(map[string]Option, len(declared))
	values := make(map[string]string, len(declared))
	for _, o := range declared {
		byName[strings.ToLower(o.Name)] = o
		values[o.Name] = o.Default
	}

	for _, k := range sortedKeys(overrides) {
		o, ok := byName[strings.ToLower(k)]
		if !ok {
			return nil, fmt.Errorf("unknown option %q", k)
		}
		if err := o.validate(overrides[k]); err != nil {
			return nil, fmt.Errorf("option %q: %w", k, err)
		}
		values[o.Name] = overrides[k]
	}
	return values, nil
}

// substitute replaces $(name) placeholders in the data-bearing fields of a plugin's
// actions with the resolved option values. Templating is bounded to the value data of
// registry and text edits — an AddReg value and the value of a text SetValue/AddLine/
// DelLine — never a key, subkey, section, or path; that keeps the surface explicit and a
// mistyped placeholder from silently rewriting structure. A placeholder naming an option
// the plugin did not declare is an error (a plugin typo, surfaced rather than shipped as
// literal text). The input slice is not modified.
//
// A plugin that declares no options has no placeholder contract, so substitution is
// skipped entirely: $(...) is reserved only for plugins that opt in, and the common
// no-option plugin pays neither the copy nor the scan.
func substitute(acts []action.Action, values map[string]string) ([]action.Action, error) {
	if len(values) == 0 {
		return acts, nil
	}
	lower := make(map[string]string, len(values))
	for k, v := range values {
		lower[strings.ToLower(k)] = v
	}

	out := make([]action.Action, len(acts))
	copy(out, acts)
	for i := range out {
		var fields []*string
		switch out[i].Kind {
		case action.AddReg:
			fields = []*string{&out[i].Data}
		case action.SetValue, action.AddLine, action.DelLine:
			fields = []*string{&out[i].LineData}
		}
		for _, p := range fields {
			v, err := expandPlaceholders(*p, lower)
			if err != nil {
				return nil, err
			}
			*p = v
		}
	}
	return out, nil
}

// expandPlaceholders replaces every $(name) in s with values[name] (name matched
// lowercased). A $(...) whose name is not in values, or an unterminated $(, is an error —
// the placeholder syntax is reserved for options, so an unresolved one is a mistake, not
// text to keep. A string with no "$(" is returned unchanged.
func expandPlaceholders(s string, values map[string]string) (string, error) {
	if !strings.Contains(s, "$(") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '(' {
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				return "", fmt.Errorf("unterminated option placeholder in %q", s)
			}
			name := strings.TrimSpace(s[i+2 : i+2+end])
			v, ok := values[strings.ToLower(name)]
			if !ok {
				return "", fmt.Errorf("value %q references undeclared option %q", s, name)
			}
			b.WriteString(v)
			i += 2 + end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

// sortedKeys returns m's keys in ascending order, for deterministic iteration.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
