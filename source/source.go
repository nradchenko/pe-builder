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

// Area names which of a source's two top-level folders a file belongs to.
//
// A Windows installation source keeps its files in two areas that happen to be the
// same folder on x86 media and are distinct folders on amd64 media, where the OS
// payload moves to AMD64 while the real-mode boot chain stays behind in I386. A
// file's area governs both which source folder it is read from and which output
// folder it is written to; the two are always the same, so a tree never mixes them.
type Area int

const (
	// AreaSystem holds the OS payload: the setup metadata (txtsetup.sif,
	// layout.inf), the base hives, and everything under system32. It follows the
	// source's architecture.
	AreaSystem Area = iota
	// AreaBoot holds the real-mode boot chain the CD boot sector hands control to:
	// the setup loader, the hardware detector, and the boot prompt. Those run
	// before any 64-bit code does, so they ship in I386 on every architecture.
	AreaBoot
)

// String renders the area for display and test failures.
func (a Area) String() string {
	switch a {
	case AreaBoot:
		return "boot"
	case AreaSystem:
		return "system"
	}
	return fmt.Sprintf("area(%d)", int(a))
}

// bootRoot is the folder the real-mode boot chain lives in, on every architecture.
// It is a fixed name rather than a derived one because the components in it are
// real-mode and so are never architecture-specific.
const bootRoot = "i386"

// defaultArch is the architecture assumed for an Identity that carries none, so a
// zero Identity still resolves to the x86 layout. Resolving it here rather than at
// each reader keeps one definition of the fallback, so no caller can drift from it.
const defaultArch = "i386"

// ResolveArch normalizes an architecture for everything that selects on one: an unset
// architecture is the x86 layout. Both halves of that selection go through it — the
// folders a build reads and writes (Roots) and the architecture-conditional sections
// it compiles — so the two cannot disagree about what an unset architecture means.
func ResolveArch(arch string) string {
	if arch == "" {
		return defaultArch
	}
	return arch
}

// KnownArch reports whether arch is one Probe can detect. It is the check for an
// architecture that came from somewhere other than a probed source — a hand-built
// Identity or a command-line flag — where a value that matches nothing would
// otherwise select no architecture-specific content and say nothing about it.
func KnownArch(arch string) bool {
	for _, a := range archFolders {
		if a.arch == arch {
			return true
		}
	}
	return false
}

// Roots is a source's two top-level folders, resolved for one architecture. It is
// the single definition of "which folder", shared by the layout parser, the tree
// assembler, the hive stage, and the boot stage — all of which resolve a folder
// through it rather than naming one, so they cannot disagree about where a source
// keeps its files.
type Roots struct {
	// System is the folder for AreaSystem: the architecture's own folder.
	System string
	// Boot is the folder for AreaBoot; always i386.
	Boot string
}

// For returns the folder that holds the given area. An area this function does not
// know resolves to the system folder: every area but the boot chain is part of the
// OS payload, so that is the safe direction for one that has been added without
// updating this switch.
func (r Roots) For(a Area) string {
	switch a {
	case AreaBoot:
		return r.Boot
	case AreaSystem:
		return r.System
	}
	return r.System
}

// Identity describes a source as detected by Probe.
type Identity struct {
	// Build is the Windows build number, e.g. 2600 (XP) or 3790 (2003/x64).
	Build int
	// Arch is the processor architecture, and doubles as the name of the source's
	// architecture folder (see Roots). Probe is the only producer, and emits exactly
	// "i386" or "amd64"; an Identity assembled by hand is expected to honor that.
	Arch string
	// Flavor is the source flavor: "winxp", "win2003", ...
	Flavor string
}

// String renders the identity for display.
func (id Identity) String() string {
	return fmt.Sprintf("%s build %d (%s)", id.Flavor, id.Build, id.Arch)
}

// Roots reports the source's two top-level folders. The system area follows the
// probed architecture (an i386 source keeps both areas in the one folder, an amd64
// source splits them); the boot area is always i386. An Identity with no
// architecture resolves to the x86 layout.
func (id Identity) Roots() Roots {
	return Roots{System: ResolveArch(id.Arch), Boot: bootRoot}
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

// RequireImageSource reports whether the source at path can supply a boot image:
// it must be an ISO file, not a directory. It lets a bootable build reject a
// directory source up front, before assembling a whole tree. Ownership of "is this
// an ISO?" stays in this package.
func RequireImageSource(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open source image %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory", path)
	}
	return nil
}

// OpenImageFile opens the raw bytes of an ISO source, for the on-disc structures
// the fs.FS abstraction does not expose — the El Torito boot record and catalog the
// boot image lives in. It errors on a directory source, which has no boot image.
// The caller closes the returned file.
func OpenImageFile(path string) (*os.File, error) {
	if err := RequireImageSource(path); err != nil {
		return nil, err
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
