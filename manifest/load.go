// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/nradchenko/pe-builder/internal/enginehash"
)

// raw* types mirror the HCL schema for gohcl decoding. They stay unexported so the
// public API carries no hcl tags or pointers; conversion applies defaults.
type rawManifest struct {
	Source     rawSource   `hcl:"source,block"`
	Output     rawOutput   `hcl:"output,block"`
	Build      *rawBuild   `hcl:"build,block"`
	Layout     string      `hcl:"layout,optional"`
	PluginsDir string      `hcl:"plugins_dir,optional"`
	Plugins    []rawPlugin `hcl:"plugin,block"`
}

type rawSource struct {
	Path string `hcl:"path"`
	Type string `hcl:"type,optional"`
}

type rawOutput struct {
	Target string `hcl:"target,optional"`
	// Path is optional in the schema because it is required only for a target that
	// writes a file; Validate is what enforces it per target. Declaring it required
	// here would reject a legitimate "none"-target manifest during decode, before
	// Validate ever runs.
	Path        string `hcl:"path,optional"`
	VolumeLabel string `hcl:"volume_label,optional"`
}

type rawBuild struct {
	WorkDir           string `hcl:"work_dir,optional"`
	KeepWork          bool   `hcl:"keep_work,optional"`
	AllowPluginBuilds bool   `hcl:"allow_plugin_builds,optional"`
}

type rawPlugin struct {
	Name     string       `hcl:"name,label"`
	Enabled  *bool        `hcl:"enabled,optional"`
	Options  *rawOptions  `hcl:"options,block"`
	Files    *rawFiles    `hcl:"files,block"`
	Registry *rawRegistry `hcl:"registry,block"`
}

type rawOptions struct {
	// Remain captures the arbitrary key = value attributes of an options block.
	Remain hcl.Body `hcl:",remain"`
}

type rawFiles struct {
	Include []string    `hcl:"include,optional"`
	Exclude []string    `hcl:"exclude,optional"`
	Inject  []rawInject `hcl:"inject,optional"`
}

// rawInject uses cty tags because inject is an attribute list of objects.
type rawInject struct {
	From string `cty:"from"`
	To   string `cty:"to"`
}

type rawRegistry struct {
	Set    map[string]string `hcl:"set,optional"`
	Delete []string          `hcl:"delete,optional"`
}

// Load reads, decodes, and defaults the manifest at path. It does not run semantic
// validation; call Validate for that.
func Load(path string) (*Manifest, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return Decode(src, path)
}

// Decode parses and defaults manifest source bytes. filename is recorded as the
// manifest's path and used in diagnostics.
func Decode(src []byte, filename string) (*Manifest, error) {
	file, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, diags
	}

	var raw rawManifest
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, diags
	}

	m, err := toManifest(&raw)
	if err != nil {
		return nil, err
	}
	// Fingerprint the source bytes, not the decoded value: it is what a later build
	// receipt records, and hashing the text means a change that decodes to the same
	// manifest still reads as a different manifest.
	m.Path = filename
	m.Digest = enginehash.SumBytes(src)
	return m, nil
}

// toManifest maps the decoded raw manifest to the public type, applying defaults.
func toManifest(raw *rawManifest) (*Manifest, error) {
	m := &Manifest{
		Source: Source{
			Path: raw.Source.Path,
			Type: orDefault(raw.Source.Type, DefaultSourceType),
		},
		Output: Output{
			Target:      orDefault(raw.Output.Target, DefaultTarget),
			Path:        raw.Output.Path,
			VolumeLabel: orDefault(raw.Output.VolumeLabel, DefaultVolumeLabel),
		},
		Layout:     orDefault(raw.Layout, DefaultLayout),
		PluginsDir: raw.PluginsDir,
	}
	if raw.Build != nil {
		m.Build = Build{
			WorkDir:           raw.Build.WorkDir,
			KeepWork:          raw.Build.KeepWork,
			AllowPluginBuilds: raw.Build.AllowPluginBuilds,
		}
	}

	for _, rp := range raw.Plugins {
		p := Plugin{
			Name:    rp.Name,
			Enabled: rp.Enabled == nil || *rp.Enabled, // default: enabled
		}

		opts, err := decodeOptions(rp.Options)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", rp.Name, err)
		}
		p.Options = opts

		if rp.Files != nil {
			p.Files = Files{Include: rp.Files.Include, Exclude: rp.Files.Exclude}
			for _, in := range rp.Files.Inject {
				p.Files.Inject = append(p.Files.Inject, Inject(in))
			}
		}
		if rp.Registry != nil {
			p.Registry = Registry{Set: rp.Registry.Set, Delete: rp.Registry.Delete}
		}

		m.Plugins = append(m.Plugins, p)
	}

	return m, nil
}

// decodeOptions flattens an options block's arbitrary attributes into a string map.
func decodeOptions(o *rawOptions) (map[string]string, error) {
	if o == nil {
		return nil, nil
	}
	attrs, diags := o.Remain.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}
	if len(attrs) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(attrs))
	for name, attr := range attrs {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return nil, diags
		}
		s, err := convert.Convert(val, cty.String)
		if err != nil {
			return nil, fmt.Errorf("option %q: must be a string: %w", name, err)
		}
		out[name] = s.AsString()
	}
	return out, nil
}

// Resolve returns the manifest path to use: arg when non-empty, otherwise
// DefaultManifestName if it exists in the current directory.
func Resolve(arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	if _, err := os.Stat(DefaultManifestName); err == nil {
		return DefaultManifestName, nil
	}
	return "", fmt.Errorf("no manifest given and %s not found in the current directory", DefaultManifestName)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
