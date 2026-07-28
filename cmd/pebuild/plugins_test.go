// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/plugin"
)

// TestPrintPluginOptions covers what `plugins info` reports about a plugin's
// manifest-settable knobs. The declaration lives in an embedded .inf, so this output is
// the only copy a user who installed a release binary can read — an option missing here is
// an option they cannot discover.
func TestPrintPluginOptions(t *testing.T) {
	opts := []plugin.Option{
		{Name: "width", Default: "1024", Type: plugin.OptionInt},
		{Name: "bpp", Default: "32", Type: plugin.OptionInt, Allowed: []string{"8", "16", "24", "32"}},
		{Name: "color", Default: "NormalColor", Allowed: []string{"NormalColor", "Metallic"}},
	}
	var buf bytes.Buffer
	printPluginOptions(&buf, opts)
	out := buf.String()

	for _, want := range []string{
		"Options: 3",
		"width", "1024",
		"bpp", "8, 16, 24, 32",
		"color", "NormalColor, Metallic",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}

	// An omitted type is a string; the listing must say so rather than leave the column
	// blank, since "no type" and "string" are not the same claim to a reader.
	if !strings.Contains(out, string(plugin.OptionString)) {
		t.Errorf("an untyped option should report as %q\n%s", plugin.OptionString, out)
	}

	// Trailing whitespace on rows that constrain nothing is what dropping the empty
	// allowed-set cell avoids; assert it stays dropped.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

// TestPrintPluginOptionsAlignment uses the one ordering that exposes the bug: an option
// constraining nothing *between* two that do. Order matters here — a fixture putting the
// unconstrained option first passes while the real listing is misaligned.
func TestPrintPluginOptionsAlignment(t *testing.T) {
	var buf bytes.Buffer
	printPluginOptions(&buf, []plugin.Option{
		{Name: "bpp", Default: "32", Type: plugin.OptionInt, Allowed: []string{"8", "32"}},
		{Name: "a_very_long_option_name", Default: "1024", Type: plugin.OptionInt},
		{Name: "color", Default: "NormalColor", Allowed: []string{"NormalColor", "Metallic"}},
	})

	var cols []int
	for _, line := range strings.Split(buf.String(), "\n") {
		if i := strings.Index(line, "8, 32"); i >= 0 {
			cols = append(cols, i)
		}
		if i := strings.Index(line, "NormalColor, Metallic"); i >= 0 {
			cols = append(cols, i)
		}
	}
	if len(cols) != 2 {
		t.Fatalf("expected both allowed sets on their own lines, got %d\n%s", len(cols), buf.String())
	}
	if cols[0] != cols[1] {
		t.Errorf("allowed sets misaligned: columns %d and %d\n%s", cols[0], cols[1], buf.String())
	}
}

// TestPrintPluginOptionsEmpty asserts a plugin declaring no options prints nothing at all.
// An "Options: 0" heading would read as a plugin whose options failed to parse.
func TestPrintPluginOptionsEmpty(t *testing.T) {
	var buf bytes.Buffer
	printPluginOptions(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("want no output for a plugin with no options, got %q", buf.String())
	}
}
