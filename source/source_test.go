// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
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

func TestOpenMissing(t *testing.T) {
	src, err := Open(t.TempDir() + "/does-not-exist")
	if err == nil {
		t.Error("Open of a missing path returned no error")
	}
	if src != nil {
		t.Errorf("Open returned a non-nil source on error: %v", src)
	}
}
