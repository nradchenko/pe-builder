// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// Validate checks a decoded manifest for semantic errors, returning all problems
// found joined into a single error (nil when the manifest is valid).
func Validate(m *Manifest) error {
	var errs []error

	if m.Source.Path == "" {
		errs = append(errs, errors.New("source.path is required"))
	}
	if !slices.Contains(SourceTypes, m.Source.Type) {
		errs = append(errs, fmt.Errorf("source.type %q is invalid (want one of %v)", m.Source.Type, SourceTypes))
	}

	if m.Output.Path == "" {
		errs = append(errs, errors.New("output.path is required"))
	}
	if !slices.Contains(Targets, m.Output.Target) {
		errs = append(errs, fmt.Errorf("output.target %q is invalid (want one of %v)", m.Output.Target, Targets))
	}
	if n := utf8.RuneCountInString(m.Output.VolumeLabel); n > maxVolumeLabelLen {
		errs = append(errs, fmt.Errorf("output.volume_label is %d characters (max %d)", n, maxVolumeLabelLen))
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
