// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"strings"
)

// ValidateDepends checks the vendor [Update] depends relationships across the enabled
// set: every plugin a selected plugin depends on must itself be selected (enabled). A
// depends target is matched case-insensitively against both the plugins' names and
// their [Update] id values. An unmet dependency is an error naming the dependent and
// the missing target — there is no auto-enable, so enabling a dependency is an
// explicit choice. [Update] parent is a customization-association hint and is not
// enforced here.
func ValidateDepends(selected []*Discovered) error {
	available := make(map[string]bool, len(selected)*2)
	for _, d := range selected {
		available[strings.ToLower(d.Name)] = true
		if id := d.Update().ID; id != "" {
			available[strings.ToLower(id)] = true
		}
	}

	var missing []string
	for _, d := range selected {
		for _, dep := range d.Update().Depends {
			if !available[strings.ToLower(dep)] {
				missing = append(missing, fmt.Sprintf("%q requires %q", d.Name, dep))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("unmet plugin dependencies (enable them, or disable the dependent): %s",
			strings.Join(missing, "; "))
	}
	return nil
}
