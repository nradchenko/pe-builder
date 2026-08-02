// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package target defines the pluggable output backend interface and a registry of
// available backends, and provides the ISO backend that masters an assembled tree
// into an El Torito no-emulation bootable image (see iso.go).
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
	// Emit writes the artifact for the tree rooted at treeDir. A target may consume
	// treeDir (the ISO master hands it to go-diskfs, which removes it once mastered),
	// so the caller must not rely on the tree surviving the call and must copy it
	// first if it needs to keep it.
	//
	// A target publishes its artifact only once it is complete: on failure it must
	// leave whatever was at OutputPath before untouched rather than a partial
	// artifact, which a later run or burn cannot tell from a good one.
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
