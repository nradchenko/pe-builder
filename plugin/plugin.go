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
	// Actions is the compiled build-IR for the selected build.
	Actions []action.Action
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
// (conventionally the .inf base name). build selects .BuildNo section variants
// (0 = unsuffixed sections only). A valid plugin must carry the [Version]
// signature and a [PEBuilder] section.
func Parse(name string, f *inf.File, build int) (*Plugin, error) {
	if err := checkSignature(f); err != nil {
		return nil, err
	}
	if !f.HasSection("PEBuilder") {
		return nil, fmt.Errorf("plugin %q: missing [PEBuilder] section", name)
	}

	p := &Plugin{Name: name}
	pb := sectionMap(f.Section("PEBuilder", build))
	p.Display = pb["name"]
	p.Help = pb["help"]
	p.Config = pb["config"]
	if v, ok := pb["enable"]; ok {
		p.Enabled = strings.TrimSpace(v) == "1"
	}

	p.Update = parseUpdate(f, build)

	actions, err := Actions(f, build)
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
