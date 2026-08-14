// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"errors"
	"fmt"
	"slices"
)

// Validate checks a decoded manifest for semantic errors, returning all problems
// found joined into a single error (nil when the manifest is valid).
func Validate(m *Manifest) error {
	var errs []error

	if m.Source.Path == "" {
		errs = append(errs, errors.New("source.path is required"))
	}
	if !slices.Contains(Targets, m.Output.Target) {
		errs = append(errs, fmt.Errorf("output.target %q is invalid (want one of %v)", m.Output.Target, Targets))
	}
	if !slices.Contains(Layouts, m.Layout) {
		errs = append(errs, fmt.Errorf("layout %q is invalid (want one of %v)", m.Layout, Layouts))
	}
	// A WIM layout is a RAM-booted image mastered onto an iso; it has
	// no meaning for the tree-only "none" target, so require the iso target explicitly.
	if m.Layout == LayoutWIM && m.Output.Target != TargetISO {
		errs = append(errs, fmt.Errorf("layout %q requires output.target %q", m.Layout, TargetISO))
	}
	// A path is required only for a target that writes a file; the "none" target
	// (tree/hives only) needs none.
	if m.Output.Path == "" && m.Output.Target == TargetISO {
		errs = append(errs, errors.New("output.path is required for the iso target"))
	}
	// The ISO9660 volume identifier is a fixed 32-byte field, so the limit is on
	// bytes, not runes — a multibyte label within 32 runes could still overrun 32
	// bytes and be truncated mid-character.
	if n := len(m.Output.VolumeLabel); n > maxVolumeLabelLen {
		errs = append(errs, fmt.Errorf("output.volume_label is %d bytes (max %d)", n, maxVolumeLabelLen))
	}

	seen := make(map[string]struct{}, len(m.Plugins))
	for _, p := range m.Plugins {
		if p.Name == "" {
			errs = append(errs, errors.New("plugin block is missing a name"))
			continue
		}
		if _, dup := seen[p.Name]; dup {
			errs = append(errs, fmt.Errorf("duplicate plugin %q", p.Name))
		}
		seen[p.Name] = struct{}{}
	}

	return errors.Join(errs...)
}
