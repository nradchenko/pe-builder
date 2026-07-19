// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/internal/golden"
)

// parseOptionsINF parses INF text and returns the plugin's declared options.
func parseOptionsINF(t *testing.T, body string) ([]Option, error) {
	t.Helper()
	f, err := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	return parseOptions(f, 0)
}

func TestParseOptions(t *testing.T) {
	opts, err := parseOptionsINF(t, "[Options]\n"+
		"width = 1024, int\n"+
		"bpp   = 32, int, 8, 16, 24, 32\n"+
		"label = classic\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 3 {
		t.Fatalf("got %d options, want 3", len(opts))
	}
	if opts[0].Name != "width" || opts[0].Default != "1024" || opts[0].Type != OptionInt {
		t.Errorf("width = %+v", opts[0])
	}
	if opts[1].Type != OptionInt || strings.Join(opts[1].Allowed, ",") != "8,16,24,32" {
		t.Errorf("bpp = %+v", opts[1])
	}
	// A declaration with no explicit type defaults to string with no allowed set.
	if opts[2].Type != OptionString || len(opts[2].Allowed) != 0 {
		t.Errorf("label = %+v, want string with no allowed set", opts[2])
	}
}

func TestParseOptionsErrors(t *testing.T) {
	cases := map[string]string{
		"unknown type":        "[Options]\nx = 1, frobnicate\n",
		"duplicate name":      "[Options]\nx = 1\nx = 2\n",
		"bad int default":     "[Options]\nx = notnum, int\n",
		"default not allowed": "[Options]\nx = 64, int, 8, 16, 32\n",
	}
	for name, body := range cases {
		if _, err := parseOptionsINF(t, body); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestResolveOptionsDefaultAndOverride(t *testing.T) {
	declared := []Option{
		{Name: "width", Default: "1024", Type: OptionInt},
		{Name: "bpp", Default: "32", Type: OptionInt, Allowed: []string{"8", "16", "24", "32"}},
	}
	// No override: defaults apply.
	values, err := ResolveOptions(declared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["width"] != "1024" || values["bpp"] != "32" {
		t.Errorf("defaults = %v, want width=1024 bpp=32", values)
	}
	// Override one option; the other keeps its default. Override key matches case-insensitively.
	values, err = ResolveOptions(declared, map[string]string{"WIDTH": "1280"})
	if err != nil {
		t.Fatal(err)
	}
	if values["width"] != "1280" || values["bpp"] != "32" {
		t.Errorf("override = %v, want width=1280 bpp=32", values)
	}
}

func TestResolveOptionsRejects(t *testing.T) {
	declared := []Option{
		{Name: "bpp", Default: "32", Type: OptionInt, Allowed: []string{"8", "16", "24", "32"}},
	}
	cases := map[string]map[string]string{
		"unknown option":  {"height": "600"},
		"not an integer":  {"bpp": "deep"},
		"outside allowed": {"bpp": "17"},
	}
	for name, overrides := range cases {
		if _, err := ResolveOptions(declared, overrides); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestResolveOptionsIntAllowedNumeric(t *testing.T) {
	// An int option compares its allowed set numerically: 0x20 satisfies "32".
	declared := []Option{{Name: "bpp", Default: "32", Type: OptionInt, Allowed: []string{"8", "16", "24", "32"}}}
	if _, err := ResolveOptions(declared, map[string]string{"bpp": "0x20"}); err != nil {
		t.Errorf("0x20 rejected against allowed [8 16 24 32]: %v", err)
	}
}

func TestSubstitute(t *testing.T) {
	acts := []action.Action{
		{Kind: action.AddReg, Subkey: "Display\\Mode", ValueName: "Width", Data: "$(width)"},
		{Kind: action.SetValue, TargetFile: "d.ini", LineData: "$(label) $(width)x$(height)"},
		// A key/subkey field is never templated, only value data.
		{Kind: action.AddReg, Subkey: "$(width)", ValueName: "X", Data: "plain"},
	}
	values := map[string]string{"width": "1280", "height": "1024", "label": "wide"}
	out, err := substitute(acts, values)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Data != "1280" {
		t.Errorf("AddReg Data = %q, want 1280", out[0].Data)
	}
	if out[1].LineData != "wide 1280x1024" {
		t.Errorf("SetValue LineData = %q, want \"wide 1280x1024\"", out[1].LineData)
	}
	if out[2].Subkey != "$(width)" {
		t.Errorf("Subkey = %q, want it left untemplated", out[2].Subkey)
	}
	// The input slice is not mutated.
	if acts[0].Data != "$(width)" {
		t.Errorf("input mutated: acts[0].Data = %q", acts[0].Data)
	}
}

func TestSubstituteNoOptionsLeavesPlaceholdersLiteral(t *testing.T) {
	// A plugin that declares no options has no placeholder contract: $(...) is reserved
	// only for opted-in plugins, so a literal "$(" in a value passes through untouched
	// rather than failing the build.
	acts := []action.Action{
		{Kind: action.AddReg, Data: "$(notanoption)"},
		{Kind: action.SetValue, LineData: "echo $(date)"},
	}
	out, err := substitute(acts, nil)
	if err != nil {
		t.Fatalf("substitute with no options errored on literal $(: %v", err)
	}
	if out[0].Data != "$(notanoption)" || out[1].LineData != "echo $(date)" {
		t.Errorf("literal placeholders were altered: %+v", out)
	}
}

func TestSubstituteErrors(t *testing.T) {
	values := map[string]string{"width": "1024"}
	cases := map[string]action.Action{
		"undeclared placeholder":   {Kind: action.AddReg, Data: "$(height)"},
		"unterminated placeholder": {Kind: action.AddReg, Data: "$(width"},
	}
	for name, a := range cases {
		if _, err := substitute([]action.Action{a}, values); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// renderActions dumps a compiled action list to the stable form used by golden tests.
func renderActions(acts []action.Action) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Actions (%d):\n", len(acts))
	for _, a := range acts {
		fmt.Fprintf(&b, "  %s\n", renderAction(a))
	}
	return b.String()
}

// discoverFixture parses a fixture INF into a single-unit Discovered, as discovery would.
func discoverFixture(t *testing.T, path string) *Discovered {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := inf.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("options", f, Target{})
	if err != nil {
		t.Fatal(err)
	}
	return &Discovered{Name: "options", Units: []*Plugin{p}}
}

func TestCompileOptionsDefaultGolden(t *testing.T) {
	// No override: the declared defaults substitute into the templated values.
	d := discoverFixture(t, "testdata/options/options.inf")
	acts, _, err := Compile([]Selected{{Plugin: d}}, nil, byTarget)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "options-default", renderActions(acts))
}

func TestCompileOptionsOverrideGolden(t *testing.T) {
	// A manifest override changes the resolution and label; bpp keeps its default.
	d := discoverFixture(t, "testdata/options/options.inf")
	sel := []Selected{{Plugin: d, Override: Override{Options: map[string]string{
		"width":  "1280",
		"height": "1024",
		"label":  "wide",
	}}}}
	acts, _, err := Compile(sel, nil, byTarget)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "options-override", renderActions(acts))
}

func TestCompileOptionsRejectsBadOverride(t *testing.T) {
	d := discoverFixture(t, "testdata/options/options.inf")
	cases := map[string]map[string]string{
		"unknown option":  {"depth": "8"},
		"outside allowed": {"bpp": "17"},
	}
	for name, overrides := range cases {
		sel := []Selected{{Plugin: d, Override: Override{Options: overrides}}}
		if _, _, err := Compile(sel, nil, byTarget); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}
