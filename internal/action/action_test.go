// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package action

import "testing"

func TestKindValid(t *testing.T) {
	for _, k := range Kinds {
		if !k.Valid() {
			t.Errorf("Kind %q reported invalid but is in Kinds", k)
		}
	}
	for _, k := range []Kind{"", "bogus", "COPY_FILE"} {
		if k.Valid() {
			t.Errorf("Kind %q reported valid but is not defined", k)
		}
	}
}
