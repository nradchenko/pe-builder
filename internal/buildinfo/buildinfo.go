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

// String renders a single human-readable build-info line.
func String() string {
	return fmt.Sprintf("pebuild %s (commit %s, built %s)", Version, Commit, Date)
}
