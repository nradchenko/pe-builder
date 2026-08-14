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
	DefaultTarget      = "iso"
	DefaultVolumeLabel = "PEBUILDER"
	DefaultLayout      = LayoutClassic
	// maxVolumeLabelLen is the ISO9660 volume identifier length limit.
	maxVolumeLabelLen = 32
)

// Output target names.
const (
	// TargetISO masters a bootable ISO at output.path.
	TargetISO = "iso"
	// TargetNone stops after the file tree and hives, producing no media.
	TargetNone = "none"
)

// Boot layouts. The layout selects how the source is turned into a booting image.
const (
	// LayoutClassic masters the assembled tree directly onto a CDBOOT ISO — the
	// installed-on-media PE the source's own boot chain loads.
	LayoutClassic = "classic"
	// LayoutWIM is the distributable WIM boot chain: grub4dos maps a small boot-core image
	// (coreimg) into RAM as a CD and boots it with an unmodified setupldr; firadisk reclaims
	// that RAM region and the wimmf overlay serves the rest of the tree from the compressed WIM
	// on the real CD. It ships no Microsoft boot binary, and the boot device is a property of
	// the boot-chain plugin the layout enables — currently the grub4dos CD chain. Requires the
	// WIM-boot infrastructure plugins (grub4dos, firadisk, wimmf) and an iso target.
	LayoutWIM = "wim"
)

// Targets and Layouts enumerate the values accepted in v0.
var (
	Targets = []string{TargetISO, TargetNone}
	Layouts = []string{LayoutClassic, LayoutWIM}
)

// Manifest is a fully decoded, defaulted build manifest.
type Manifest struct {
	Source Source
	Output Output
	Build  Build
	// Layout selects the boot layout: "classic" (default) or "wim". It is
	// orthogonal to Output.Target — a WIM layout still produces an iso, but built
	// a different way and carrying a different boot chain.
	Layout     string
	PluginsDir string
	Plugins    []Plugin
	// Path is the file the manifest was read from, empty when it was decoded from
	// bytes with no file behind them. It is informational: a manifest that moved or
	// was renamed is still the same manifest.
	Path string
	// Digest is "sha256:<hex>" over the manifest source bytes — the identity a build
	// records, so a build can be told apart from one made with an edited manifest.
	Digest string
}

// Source describes the Windows installation source to build from.
//
// It is the path alone: what the media are — build, architecture, product type — is
// read from the media themselves, so a manifest neither states it nor overrides it.
type Source struct {
	// Path is a directory or an .iso file (auto-detected).
	Path string
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
	// AllowPluginBuilds opts in to running a user-supplied plugin's [Build] command,
	// which executes an arbitrary shell command. Built-in plugins build regardless;
	// this gate only affects plugins from a drop-in directory.
	AllowPluginBuilds bool
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

// DisabledPluginNames returns the names of the plugins carrying an explicit
// `enabled = false` block, in manifest order. These are distinct from plugins the
// manifest never mentions: a required dependency that is explicitly disabled is a
// conflict the resolver rejects, whereas an unmentioned dependency is auto-enabled.
func (m *Manifest) DisabledPluginNames() []string {
	var out []string
	for _, p := range m.Plugins {
		if !p.Enabled {
			out = append(out, p.Name)
		}
	}
	return out
}
