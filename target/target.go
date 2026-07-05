// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package target defines the pluggable output backend interface and a registry of
// available backends. The ISO backend is the first concrete target and arrives
// later; this package currently defines the interface and registry.
package target

import (
	"context"
	"sort"
)

// Options holds the settings passed to a target when it emits its artifact. It
// grows as targets need more knobs.
type Options struct {
	// OutputPath is where the artifact is written.
	OutputPath string
	// VolumeLabel is the volume identifier for media-like targets.
	VolumeLabel string
}

// Target is an output backend that turns an assembled tree into an artifact.
type Target interface {
	// Name is the target's stable identifier, e.g. "iso".
	Name() string
	// Emit writes the artifact for the tree rooted at treeDir.
	Emit(ctx context.Context, treeDir string, opts Options) error
}

// registry holds the registered output backends by name.
var registry = map[string]Target{}

// Register adds a target to the registry, replacing any existing target with the
// same name. It is intended to be called from backend package init functions.
func Register(t Target) {
	registry[t.Name()] = t
}

// Get returns the registered target with the given name.
func Get(name string) (Target, bool) {
	t, ok := registry[name]
	return t, ok
}

// Names returns the names of all registered targets, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
