// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/manifest"
)

// TestPrintSummaryOutputLine covers how the summary reports what a build produces.
// The "none" target writes no media, so it must not be reported as writing a file —
// including when the manifest still carries a path, which is the shape every manifest
// written before output.path became optional has, and which Validate accepts.
func TestPrintSummaryOutputLine(t *testing.T) {
	tests := []struct {
		name   string
		out    manifest.Output
		want   string
		absent string
	}{
		{
			name: "iso reports the file it writes",
			out:  manifest.Output{Target: manifest.TargetISO, Path: "./out/pe.iso", VolumeLabel: "PEBUILDER"},
			want: "Output:   iso -> ./out/pe.iso (label PEBUILDER)",
		},
		{
			name: "none with no path",
			out:  manifest.Output{Target: manifest.TargetNone, VolumeLabel: "PEBUILDER"},
			want: "Output:   none (file tree only; no media)",
		},
		{
			// The regression: keyed on an empty path, this printed
			// "none -> ./out/pe.iso", promising a file the build never writes.
			name:   "none still carrying a path",
			out:    manifest.Output{Target: manifest.TargetNone, Path: "./out/pe.iso", VolumeLabel: "PEBUILDER"},
			want:   "Output:   none (file tree only; no media)",
			absent: "./out/pe.iso",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &manifest.Manifest{
				Source: manifest.Source{Path: "/isos/winxp.iso"},
				Output: tt.out,
				Layout: manifest.LayoutClassic,
			}
			var b bytes.Buffer
			if err := printSummary(&b, "pebuild.hcl", m); err != nil {
				t.Fatalf("printSummary: %v", err)
			}
			got := b.String()
			if !strings.Contains(got, tt.want) {
				t.Errorf("summary does not contain %q:\n%s", tt.want, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("summary names %q, which this target never writes:\n%s", tt.absent, got)
			}
		})
	}
}

// TestPrintSummaryPlugins checks that the summary lists the enabled plugins and counts
// them against the total, so a plugin left disabled is visibly declared rather than
// silently missing.
func TestPrintSummaryPlugins(t *testing.T) {
	m := &manifest.Manifest{
		Source: manifest.Source{Path: "/isos/winxp.iso"},
		Output: manifest.Output{Target: manifest.TargetISO, Path: "./o.iso", VolumeLabel: "PEBUILDER"},
		Layout: manifest.LayoutClassic,
		Plugins: []manifest.Plugin{
			{Name: "luna", Enabled: true},
			{Name: "games", Enabled: false},
		},
	}
	var b bytes.Buffer
	if err := printSummary(&b, "pebuild.hcl", m); err != nil {
		t.Fatalf("printSummary: %v", err)
	}
	got := b.String()
	if !strings.Contains(got, "Plugins:  1 enabled / 2 total") {
		t.Errorf("plugin counts wrong:\n%s", got)
	}
	if !strings.Contains(got, "  - luna") {
		t.Errorf("enabled plugin not listed:\n%s", got)
	}
	if strings.Contains(got, "  - games") {
		t.Errorf("disabled plugin listed as enabled:\n%s", got)
	}
}
