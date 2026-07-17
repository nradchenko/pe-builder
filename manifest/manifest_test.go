// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDecodeFull(t *testing.T) {
	src := `
source {
  path = "/isos/winxp.iso"
  type = "winxp"
}

output {
  target       = "iso"
  path         = "./out/rescue.iso"
  volume_label = "RESCUE"
}

build {
  work_dir  = "./.work"
  keep_work = true
}

plugins_dir = "./plugins"

plugin "disabled" {
  enabled = false
  options {
    note = "carried"
  }
}

plugin "shell" {
  enabled = true
  files {
    include = ["i386/cmd.exe"]
    exclude = ["i386/skip.dll"]
    inject  = [{ from = "./local/startnet.cmd", to = "i386/system32/startnet.cmd" }]
  }
  registry {
    set    = { "HKLM\\SYSTEM\\Setup\\CmdLine" = "cmd.exe /k startnet.cmd" }
    delete = ["HKLM\\SYSTEM\\Setup\\Old"]
  }
}
`
	got, err := Decode([]byte(src), "test.hcl")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := &Manifest{
		Source: Source{Path: "/isos/winxp.iso", Type: "winxp"},
		Output: Output{Target: "iso", Path: "./out/rescue.iso", VolumeLabel: "RESCUE"},
		Build:  Build{WorkDir: "./.work", KeepWork: true},

		PluginsDir: "./plugins",
		Plugins: []Plugin{
			{
				Name:    "disabled",
				Enabled: false,
				Options: map[string]string{"note": "carried"},
			},
			{
				Name:    "shell",
				Enabled: true,
				Files: Files{
					Include: []string{"i386/cmd.exe"},
					Exclude: []string{"i386/skip.dll"},
					Inject:  []Inject{{From: "./local/startnet.cmd", To: "i386/system32/startnet.cmd"}},
				},
				Registry: Registry{
					Set:    map[string]string{`HKLM\SYSTEM\Setup\CmdLine`: "cmd.exe /k startnet.cmd"},
					Delete: []string{`HKLM\SYSTEM\Setup\Old`},
				},
			},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Decode mismatch (-want +got):\n%s", diff)
	}
}

func TestDecodeDefaults(t *testing.T) {
	src := `
source { path = "/isos/winxp.iso" }
output { path = "./out/rescue.iso" }

plugin "p" {}
`
	got, err := Decode([]byte(src), "test.hcl")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := &Manifest{
		Source: Source{Path: "/isos/winxp.iso", Type: DefaultSourceType},
		Output: Output{Target: DefaultTarget, Path: "./out/rescue.iso", VolumeLabel: DefaultVolumeLabel},
		Plugins: []Plugin{
			{Name: "p", Enabled: true}, // enabled defaults to true
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("defaults mismatch (-want +got):\n%s", diff)
	}
}

func TestDecodeSyntaxError(t *testing.T) {
	if _, err := Decode([]byte("source { path = }"), "bad.hcl"); err == nil {
		t.Fatal("expected a decode error, got nil")
	}
}

func TestDecodeNonStringOption(t *testing.T) {
	src := `
source { path = "/x" }
output { path = "./o" }
plugin "p" {
  options { n = 3 }
}
`
	// Integers convert cleanly to strings; ensure that path works.
	got, err := Decode([]byte(src), "opt.hcl")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Plugins[0].Options["n"] != "3" {
		t.Errorf("option n = %q, want \"3\"", got.Plugins[0].Options["n"])
	}
}

func TestDecodeOptionsVerbatim(t *testing.T) {
	// The manifest layer does not interpret option names or values — it hands them to the
	// engine as written, to validate against the plugin's own [Options] declarations.
	src := `
source { path = "/x" }
output { path = "./o" }
plugin "p" {
  options {
    alpha = "one"
    beta  = "two"
  }
}
`
	got, err := Decode([]byte(src), "opt.hcl")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := map[string]string{"alpha": "one", "beta": "two"}
	if diff := cmp.Diff(want, got.Plugins[0].Options); diff != "" {
		t.Errorf("options mismatch (-want +got):\n%s", diff)
	}
}

func TestValidate(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			Source: Source{Path: "/x", Type: "auto"},
			Output: Output{Target: "iso", Path: "./o", VolumeLabel: "OK"},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string // substring; empty means expect success
	}{
		{"valid", func(*Manifest) {}, ""},
		{"missing source path", func(m *Manifest) { m.Source.Path = "" }, "source.path is required"},
		{"bad source type", func(m *Manifest) { m.Source.Type = "win95" }, "source.type"},
		{"missing output path", func(m *Manifest) { m.Output.Path = "" }, "output.path is required"},
		{"bad target", func(m *Manifest) { m.Output.Target = "usb" }, "output.target"},
		{"label too long", func(m *Manifest) { m.Output.VolumeLabel = strings.Repeat("A", 33) }, "volume_label"},
		// 20 two-byte runes = 40 bytes: within 32 runes but over the 32-byte field.
		{"label too many bytes", func(m *Manifest) { m.Output.VolumeLabel = strings.Repeat("é", 20) }, "volume_label"},
		{"duplicate plugins", func(m *Manifest) {
			m.Plugins = []Plugin{{Name: "dup"}, {Name: "dup"}}
		}, "duplicate plugin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			tt.mutate(m)
			err := Validate(m)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate: unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("Validate: expected error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("Validate: error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestExampleManifestIsValid(t *testing.T) {
	m, err := Load("../examples/rescue.hcl")
	if err != nil {
		t.Fatalf("Load example: %v", err)
	}
	if err := Validate(m); err != nil {
		t.Fatalf("Validate example: %v", err)
	}
}

func TestResolveExplicit(t *testing.T) {
	got, err := Resolve("some/manifest.hcl")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "some/manifest.hcl" {
		t.Errorf("Resolve = %q, want the explicit arg", got)
	}
}

func TestResolveMissingDefault(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir, no pebuild.hcl
	if _, err := Resolve(""); err == nil {
		t.Fatal("expected an error when no manifest is present, got nil")
	}
}

func TestEnabledPlugins(t *testing.T) {
	m := &Manifest{Plugins: []Plugin{
		{Name: "a", Enabled: true},
		{Name: "b", Enabled: false},
		{Name: "c", Enabled: true},
	}}
	got := m.EnabledPlugins()
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("EnabledPlugins returned %d plugins, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("EnabledPlugins[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}
