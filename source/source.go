// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package source abstracts a Windows installation source. A source is backed by
// either a directory or an .iso file, exposed uniformly as an fs.FS, and reports
// its identity (build, architecture, flavor).
package source

import (
	"fmt"
	"io/fs"
	"os"
)

// Backing is the read-only file store behind a source: a directory or an ISO,
// presented uniformly so the engine never depends on which.
type Backing = fs.FS

// SystemRoot is the source (and output-tree) directory that holds the
// system-root-relative files, mirroring a Windows CD's I386 folder. It is the
// single definition of that convention, shared by the source reader, the tree
// assembler, and the hive stage. (amd64 media use an AMD64 folder; supporting
// that is future work.)
const SystemRoot = "i386"

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

// source is the concrete Source: a normalized backing plus the probe logic.
type source struct {
	backing Backing
}

func (s *source) Backing() Backing { return s.backing }

// OpenImageFile opens the raw bytes of an ISO source, for the on-disc structures
// the fs.FS abstraction does not expose — the El Torito boot record and catalog the
// boot image lives in. Ownership of "is this an ISO?" stays in this package: it
// errors on a directory source, which has no boot image. The caller closes the
// returned file.
func OpenImageFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open source image %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("bootable output requires an ISO source, but %q is a directory", path)
	}
	return os.Open(path)
}

// Open returns a Source backed by the directory or .iso at path. A directory path
// is served from the filesystem; a regular file is opened as an ISO image.
func Open(path string) (Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open source %q: %w", path, err)
	}
	var backing Backing
	if info.IsDir() {
		backing, err = openDir(path)
	} else {
		backing, err = openISO(path)
	}
	if err != nil {
		return nil, fmt.Errorf("open source %q: %w", path, err)
	}
	return &source{backing: backing}, nil
}
