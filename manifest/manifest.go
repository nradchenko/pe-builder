// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package manifest defines the HCL build-manifest schema and loads, decodes, and
// validates it into a plain Go value the build engine can consume.
package manifest

// DefaultManifestName is the file looked up in the current directory when no
// manifest path is given on the command line.
const DefaultManifestName = "pebuild.hcl"

// Defaults applied to optional fields left unset in the manifest.
const (
	DefaultSourceType  = "auto"
	DefaultTarget      = "iso"
	DefaultVolumeLabel = "PEBUILDER"
	// maxVolumeLabelLen is the ISO9660 volume identifier length limit.
	maxVolumeLabelLen = 32
)

// SourceTypes and Targets enumerate the values accepted in v0.
var (
	SourceTypes = []string{"auto", "winxp", "win2003"}
	Targets     = []string{"iso"}
)

// Manifest is a fully decoded, defaulted build manifest.
type Manifest struct {
	Source     Source
	Output     Output
	Build      Build
	PluginsDir string
	Plugins    []Plugin
}

// Source describes the Windows installation source to build from.
type Source struct {
	// Path is a directory or an .iso file (auto-detected).
	Path string
	// Type is the source flavor: auto | winxp | win2003.
	Type string
}

// Output describes the artifact to produce.
type Output struct {
	// Target is the output backend; only "iso" exists in v0.
	Target string
	// Path is where the artifact is written.
	Path string
	// VolumeLabel is the ISO9660 volume identifier.
	VolumeLabel string
}

// Build holds build-time knobs.
type Build struct {
	// WorkDir is the scratch directory; empty means a system temp dir.
	WorkDir string
	// KeepWork retains the scratch directory after a build.
	KeepWork bool
}

// Plugin is a manifest-declared plugin together with its override layers.
type Plugin struct {
	// Name is the plugin's block label.
	Name string
	// Enabled reports whether the plugin participates in the build.
	Enabled bool
	// Options carries plugin-specific key/value settings (string-valued in v0).
	Options map[string]string
	// Files layers extra/suppressed/injected files over the plugin.
	Files Files
	// Registry layers registry additions/removals over the plugin.
	Registry Registry
}

// Files is a plugin's file override layer.
type Files struct {
	// Include names extra source files to pull in.
	Include []string
	// Exclude names plugin files to suppress.
	Exclude []string
	// Inject copies local files into the output tree.
	Inject []Inject
}

// Inject copies a local file to a path in the output tree.
type Inject struct {
	From string
	To   string
}

// Registry is a plugin's registry override layer.
type Registry struct {
	// Set maps fully qualified registry paths to values to write.
	Set map[string]string
	// Delete lists fully qualified registry paths to remove.
	Delete []string
}

// EnabledPlugins returns the plugins whose Enabled flag is set, in manifest order.
func (m *Manifest) EnabledPlugins() []Plugin {
	var out []Plugin
	for _, p := range m.Plugins {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}
