// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package source abstracts a Windows installation source. A source is backed by
// either a directory or an .iso file, exposed uniformly as an fs.FS, and reports
// its identity (build, architecture, flavor).
//
// The concrete directory and ISO readers arrive later; this package currently
// defines the interfaces and a stub constructor.
package source

import (
	"errors"
	"fmt"
	"io/fs"
)

// ErrNotImplemented is returned by stubs whose behavior is delivered later.
var ErrNotImplemented = errors.New("not yet implemented")

// Backing is the read-only file store behind a source: a directory or an ISO,
// presented uniformly so the engine never depends on which.
type Backing = fs.FS

// Identity describes a source as detected by Probe.
type Identity struct {
	// Build is the Windows build number, e.g. 2600 (XP) or 3790 (2003/x64).
	Build int
	// Arch is the processor architecture: "i386" or "amd64".
	Arch string
	// Flavor is the source flavor: "winxp", "win2003", ...
	Flavor string
}

// String renders the identity for display.
func (id Identity) String() string {
	return fmt.Sprintf("%s build %d (%s)", id.Flavor, id.Build, id.Arch)
}

// Source is a Windows installation source.
type Source interface {
	// Backing returns the file store behind the source.
	Backing() Backing
	// Probe detects the source's identity.
	Probe() (Identity, error)
}

// Open returns a Source backed by the directory or .iso at path.
//
// It is the constructor the CLI calls; the readers are not yet implemented, so it
// currently returns ErrNotImplemented.
func Open(path string) (Source, error) {
	return nil, fmt.Errorf("open source %q: %w", path, ErrNotImplemented)
}
