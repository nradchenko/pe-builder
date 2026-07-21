// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package plugin turns a parsed INF (see package inf) into a typed plugin
// descriptor and compiles its copy/registry/text sections into the action IR
// (package action). It models the format documented in
// docs/reference/03-plugin-format.md and the vendor [Update] extension in
// docs/reference/04-plugin-conventions.md. Actions are produced but not executed
// here.
package plugin

import (
	"fmt"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/source"
)

// Plugin is a parsed plugin: its identity and dialog metadata from [PEBuilder], the
// optional vendor [Update] relationships, and the compiled action list.
type Plugin struct {
	// Name is the plugin's identifier — by convention its .inf base name.
	Name string
	// Display is the [PEBuilder] Name= value shown in the plugin dialog.
	Display string
	// Enabled is the default enabled state ([PEBuilder] Enable=, default false).
	Enabled bool
	// Help is the [PEBuilder] Help= document filename, if any.
	Help string
	// Config is the [PEBuilder] Config= configurator command line, if any.
	Config string
	// Update holds the vendor [Update] section; its Present field reports whether
	// the section existed.
	Update Update
	// Build holds the [Build] section; its Present field reports whether the plugin
	// compiles its own payload from source at build time.
	Build BuildSpec
	// Closure holds the [Closure] section's pewalk seed modules, whose static import
	// closure the engine computes into the plugin's file set at build time.
	Closure []string
	// Options holds the [Options] section: the manifest-settable knobs (with defaults)
	// the plugin substitutes into its INF values at build time.
	Options []Option
	// Actions is the compiled build-IR for the selected build.
	Actions []action.Action
}

// BuildSpec is the [Build] section of a build-capable plugin: one that ships source
// and a build declaration rather than a prebuilt binary. Before its actions are
// evaluated, a pre-pass checks the required tools, runs the command, and captures the
// produced files as plugin-built origins.
type BuildSpec struct {
	// Present reports whether the plugin declares a [Build] section.
	Present bool
	// Requires lists executables that must be on PATH before building.
	Requires []string
	// Produces lists the file names the build emits (served as plugin-built files).
	Produces []string
	// Command is the shell command run in the build directory; defaults to "make". It
	// is taken verbatim; a ';' (an INF comment) cannot appear in it, so multi-step
	// logic belongs in the Makefile.
	Command string
	// MissingArchVariant names the architecture the plugin cannot be built for: it is
	// set when the plugin scopes [Build] by architecture but declares no variant for the
	// target. Parsing stays deliberately tolerant here so such a plugin is still
	// discovered, listed and describable — refusing it at parse time would drop it from
	// the search path entirely (Discover skips a manifest that fails to parse), so a
	// manifest naming it would report "plugin not found" instead of the real reason.
	// RunBuild is where it is refused, because that is the only operation it breaks.
	MissingArchVariant string
}

// Update is the vendor [Update] section: a per-vendor relationship/versioning
// convention (notably Sherpya's XPE), not part of Bart's documented format.
type Update struct {
	Present bool
	ID      string // stable plugin identifier
	Version string
	URL     string   // update/version-check endpoint
	Depends []string // plugins this one requires
	Parent  string   // marks this plugin a child/customization of another
}

const versionSignature = "$Windows NT$"

// Parse builds a Plugin from a parsed INF. name is the plugin's identifier
// (conventionally the .inf base name). t selects the conditional section variants:
// its build number picks .BuildNo ones (0 = unsuffixed only) and its architecture
// picks .<arch> ones. A valid plugin must carry the [Version] signature and a
// [PEBuilder] section.
func Parse(name string, f *inf.File, t Target) (*Plugin, error) {
	if err := checkSignature(f); err != nil {
		return nil, err
	}
	if !f.HasSection("PEBuilder") {
		return nil, fmt.Errorf("plugin %q: missing [PEBuilder] section", name)
	}

	p := &Plugin{Name: name}
	// These sections declare the plugin's identity and depends graph — what it *is*,
	// not what it places — so they resolve per build only; architecture variants are
	// for the sections that compile to actions. [Build] is the exception (see
	// parseBuild): it invokes a toolchain, which is arch-specific.
	pb := sectionMap(f.Section("PEBuilder", t.Build))
	p.Display = pb["name"]
	p.Help = pb["help"]
	p.Config = pb["config"]
	if v, ok := pb["enable"]; ok {
		p.Enabled = strings.TrimSpace(v) == "1"
	}

	p.Update = parseUpdate(f, t.Build)
	build, err := parseBuild(f, t)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", name, err)
	}
	p.Build = build
	p.Closure = parseClosure(f, t.Build)

	opts, err := parseOptions(f, t.Build)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", name, err)
	}
	p.Options = opts

	actions, err := Actions(f, t)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", name, err)
	}
	p.Actions = actions
	return p, nil
}

// checkSignature verifies the mandatory [Version] Signature line.
func checkSignature(f *inf.File) error {
	if !f.HasSection("Version") {
		return fmt.Errorf("missing [Version] section")
	}
	for _, l := range f.Section("Version", 0) {
		if strings.EqualFold(l.Key, "Signature") {
			// setupapi matches the signature case-insensitively.
			if strings.EqualFold(l.Field(0), versionSignature) {
				return nil
			}
			return fmt.Errorf("unexpected [Version] Signature %q", l.Field(0))
		}
	}
	return fmt.Errorf("missing [Version] Signature")
}

// parseUpdate reads the optional vendor [Update] section.
func parseUpdate(f *inf.File, build int) Update {
	if !f.HasSection("Update") {
		return Update{}
	}
	u := Update{Present: true}
	for _, l := range f.Section("Update", build) {
		switch strings.ToLower(l.Key) {
		case "id":
			u.ID = l.Field(0)
		case "version":
			u.Version = l.Field(0)
		case "url":
			u.URL = l.Field(0)
		case "parent":
			u.Parent = l.Field(0)
		case "depends":
			// depends may name several plugins, comma-separated.
			for _, dep := range l.Fields() {
				if dep = strings.TrimSpace(dep); dep != "" {
					u.Depends = append(u.Depends, dep)
				}
			}
		}
	}
	return u
}

// parseBuild reads the optional [Build] section for the target. Requires/Produces are
// space- or comma-separated lists; Command defaults to "make".
//
// [Build] is the one metadata section that is architecture-aware: it invokes a
// toolchain, which is inherently arch-specific (a cross-compiler and its target
// differ per architecture), so [Build.<arch>] folds over [Build] the same way an
// action section's arch variant does. The shared [Build] carries what is common (the
// Produces list — a driver has the same name on either arch); the arch section names
// that architecture's toolchain in Requires, so the presence check tests the compiler
// actually needed, and its Command hands the architecture to the build (e.g.
// "make ARCH=amd64"). A base [Build] is still required — it is what declares the
// plugin build-capable and holds the shared Produces.
func parseBuild(f *inf.File, t Target) (BuildSpec, error) {
	if !f.HasSection("Build") {
		// A variant without the base section it varies is almost certainly a mistake, and
		// a silent one: the plugin would be read as not build-capable, its binary never
		// compiled, and the first sign of trouble would be a missing-file error from the
		// tree assembler, nowhere near the cause. Name it here instead.
		if v := variantSections(f, "Build"); len(v) > 0 {
			return BuildSpec{}, fmt.Errorf("[%s] without a [Build] section: the base section declares "+
				"the plugin build-capable and holds the shared Produces", v[0])
		}
		return BuildSpec{}, nil
	}
	b := BuildSpec{Present: true, Command: "make"}

	// A plugin that scopes its build by architecture but declares no variant for the target
	// cannot be built for it: falling back to the base section would take its defaults — no
	// Requires, so the toolchain-presence check is skipped entirely, and Command defaulting
	// to "make", so the plugin's Makefile builds for whatever IT defaults to, staging a
	// binary of the wrong architecture with no diagnostic anywhere. Record it rather than
	// failing here (see MissingArchVariant); RunBuild refuses it. Build-number variants
	// ([Build.2600]) are not architecture scoping and do not count.
	if arch := source.ResolveArch(t.Arch); archVariants(f, "Build") > 0 && !f.HasSection("Build."+arch) {
		b.MissingArchVariant = arch
	}
	for _, l := range t.sections(f, "Build") {
		switch strings.ToLower(l.Key) {
		case "requires":
			b.Requires = append(b.Requires, tokens(l)...)
		case "produces":
			b.Produces = append(b.Produces, tokens(l)...)
		case "command":
			// Take the command verbatim (Raw), so quotes and commas survive to the
			// shell — INF field splitting would strip/truncate them. An INF ';' comment
			// still ends the line, so ';'-separated sequencing is unavailable; put
			// multi-step logic in the Makefile instead. The arch section's Command comes
			// after the base section's, so it wins — an arch build overrides the default.
			if v := strings.TrimSpace(l.Raw()); v != "" {
				b.Command = v
			}
		}
	}
	return b, nil
}

// variantSections returns the conditional variants of a section that the file declares —
// the "[<base>.<something>]" forms, whether that something is an architecture or a build
// number. It exists to catch a variant written without its base section.
func variantSections(f *inf.File, base string) []string {
	prefix := strings.ToLower(base) + "."
	var out []string
	for _, s := range f.Sections() {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// archVariants counts the variants of a section that are scoped by ARCHITECTURE, as
// distinct from the build-number variants that share the same "[<base>.<something>]"
// spelling. Only an architecture variant implies the section must be scoped for the
// architecture being built; a plugin with just a [Build.2600] is not making that claim.
func archVariants(f *inf.File, base string) int {
	prefix := strings.ToLower(base) + "."
	n := 0
	for _, s := range f.Sections() {
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		if source.KnownArch(strings.TrimPrefix(s, prefix)) {
			n++
		}
	}
	return n
}

// tokens splits an INF line's fields further on whitespace, so a Requires/Produces
// value written space-separated, comma-separated, or both yields individual tokens.
func tokens(l *inf.Line) []string {
	var out []string
	for _, f := range l.Fields() {
		out = append(out, strings.Fields(f)...)
	}
	return out
}

// sectionMap collapses a key=value section into a lowercased-key map, keeping the
// last value for a repeated key. Used for the metadata sections ([PEBuilder],
// [Update]) where each key appears once.
func sectionMap(lines []*inf.Line) map[string]string {
	m := make(map[string]string, len(lines))
	for _, l := range lines {
		if l.Key == "" {
			continue
		}
		m[strings.ToLower(l.Key)] = l.Field(0)
	}
	return m
}
