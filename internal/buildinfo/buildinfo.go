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
