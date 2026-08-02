// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package buildinfo

import "testing"

// TestVersionQuad pins the reduction a plugin's FILEVERSION depends on. The cases that
// matter are the ones the shell this replaces got wrong, and neither was visible in the
// built binary: the resource's string fields carry the version verbatim and looked
// correct while the numeric field was zero.
func TestVersionQuad(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		// The string a stamped build actually passes. `git describe` prefixes the tag,
		// and the leading "v" is what zeroed every binary before this moved into Go.
		{"git describe", "v0.5.0-23-g708fe90-dirty", "0,5,0,0"},
		{"tag", "v0.6.0", "0,6,0,0"},
		{"tag without prefix", "0.6.0", "0,6,0,0"},
		{"uppercase prefix", "V1.2.3", "1,2,3,0"},

		// Unstamped and pre-release builds have no number to report, or only part of
		// one. Both must yield a well-formed quad: the resource compiler rejects an
		// empty FILEVERSION outright.
		{"unstamped default", "dev", "0,0,0,0"},
		{"empty", "", "0,0,0,0"},
		{"prerelease", "0.5.0-rc1", "0,5,0,0"},
		{"prerelease no patch", "v2.0-beta", "2,0,0,0"},

		// The fourth component is always 0 — the project versions in three-component
		// semver, so a fourth in the input is dropped rather than reported.
		{"four components", "1.2.3.4", "1,2,3,0"},
		{"major only", "v7", "7,0,0,0"},

		// Each field is 16 bits in the resource; a component too large to fit saturates
		// rather than wrapping to a number unrelated to the version.
		{"overflow saturates", "99999.1.0", "65535,1,0,0"},

		// A bare "v" is a prefix with nothing behind it, not a component.
		{"prefix only", "v", "0,0,0,0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionQuad(tc.in); got != tc.want {
				t.Errorf("versionQuad(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestVersionQuadUsesVersion checks that the exported form reads the linker-stamped
// variable, so a stamped build cannot silently keep reporting the default.
func TestVersionQuadUsesVersion(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "v3.4.5"
	if got, want := VersionQuad(), "3,4,5,0"; got != want {
		t.Errorf("VersionQuad() = %q, want %q", got, want)
	}
}
