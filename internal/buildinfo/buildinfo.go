// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package buildinfo exposes version metadata stamped into the binary at build
// time via -ldflags. The default values let `go run`/`go test` work unstamped.
package buildinfo

import "fmt"

// These variables are overridden at link time; see the Makefile's LDFLAGS.
var (
	// Version is the release version (e.g. a git tag) or "dev" when unstamped.
	Version = "dev"
	// Commit is the short git commit hash the binary was built from.
	Commit = "none"
	// Date is the RFC 3339 UTC build timestamp.
	Date = "unknown"
)

// Attribution for the Windows VERSIONINFO resource a plugin's binaries carry. Constants rather
// than link-time variables: they identify the project, not the build. They live here so the
// several plugin binaries cannot disagree about who wrote them or under what licence — and so
// the year is edited once rather than in every plugin's resource script.
const (
	// Product is the ProductName every binary the build emits reports.
	Product = "pe-builder"
	// Company is the author, as CompanyName.
	Company = "Nikita Radchenko"
	// Copyright is LegalCopyright, licence included: a binary staged into system32 should be
	// able to say what it is licensed under without reference to the repository it came from.
	Copyright = "Copyright 2026 Nikita Radchenko. Apache-2.0"
)

// String renders a single human-readable build-info line for the named program.
func String(prog string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", prog, Version, Commit, Date)
}

// VersionQuad renders Version as the four comma-separated numbers a Windows
// VERSIONINFO resource's FILEVERSION requires — "v0.5.0-23-g708fe90-dirty" becomes
// "0,5,0,0". A plugin's build receives it as PEBUILD_VERSION_NUM and substitutes it
// straight in, so the reduction happens once here rather than in every plugin's
// Makefile, where it was shell and drifted silently.
func VersionQuad() string {
	return versionQuad(Version)
}

// versionQuad is VersionQuad's testable half. Two properties are load-bearing, and
// both were bugs in the shell it replaces:
//
//   - A leading "v" is dropped before anything else. The stamped version comes from
//     `git describe`, which prefixes the tag; truncating at the first non-numeric
//     character without removing it first discards the whole string and versions
//     every binary 0.0.0.0 — invisible, because the resource's string fields keep the
//     full version and look right.
//   - Anything the version cannot supply is zero, and a string that yields no numbers
//     at all ("dev", the unstamped default) is 0,0,0,0 rather than an error. The
//     resource compiler rejects an empty FILEVERSION, so there is no such thing as
//     omitting it.
//
// Only the first three components are read; the fourth is always 0, since the project
// versions in three-component semver and the field has nothing to say.
func versionQuad(v string) string {
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		v = v[1:]
	}
	var n [3]uint16
	for i, part := 0, 0; i < len(v) && part < len(n); i++ {
		if v[i] == '.' {
			part++
			continue
		}
		if v[i] < '0' || v[i] > '9' {
			break // a non-numeric character ends the version proper ("-rc1", "-23-gabc")
		}
		// Saturate rather than wrap: each field is 16 bits in the resource, and a
		// component that overflowed would otherwise report a number unrelated to the
		// version it came from.
		if d := uint32(n[part])*10 + uint32(v[i]-'0'); d <= 0xFFFF {
			n[part] = uint16(d)
		} else {
			n[part] = 0xFFFF
		}
	}
	return fmt.Sprintf("%d,%d,%d,0", n[0], n[1], n[2])
}
