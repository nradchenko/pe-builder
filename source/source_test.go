// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"errors"
	"testing"
)

func TestIdentityString(t *testing.T) {
	id := Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	got := id.String()
	want := "winxp build 2600 (i386)"
	if got != want {
		t.Errorf("Identity.String() = %q, want %q", got, want)
	}
}

func TestOpenNotImplemented(t *testing.T) {
	src, err := Open("/some/source")
	if src != nil {
		t.Errorf("Open returned a non-nil source: %v", src)
	}
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Open error = %v, want ErrNotImplemented", err)
	}
}
