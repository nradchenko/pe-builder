// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/plugin"
)

// closureSeed is the set of load roots the import closure grows from: the modules a
// minimal PE loads that are not themselves reached as an import of another loaded
// module. Their transitive imports (mechanism 1) are computed, so those dependencies
// need not be hand-listed; the roots themselves stay in the base manifest, which owns
// their renames (ntkrnlmp.exe → ntoskrnl.exe), fixed directories, and dual placements.
//
// This is the static half of the reviewed seed. Boot drivers are not listed here —
// they come from the source's own txtsetup.sif *.Load sections (a property of the
// source, not of pebuild) and are added to the walk at scan time — so seeding them
// pulls their framework imports (the port/class libraries) into the closure too.
//
// A root absent from a given source resolves as Missing and simply contributes no
// edges: the closure is never worse than its resolvable roots.
var closureSeed = []string{
	// Kernel, native API, and the low-level boot modules the loader maps. Both kernel
	// names are roots: the base manifest installs the multiprocessor kernel
	// (ntkrnlmp.exe) under the name ntoskrnl.exe, so listing ntoskrnl.exe as a root
	// keeps the closure from also copying the source's separate uniprocessor kernel of
	// that name over it.
	"ntkrnlmp.exe", "ntoskrnl.exe", "ntdll.dll", "bootvid.dll", "kdcom.dll",
	// The Win32 kernel subsystem.
	"win32k.sys",
	// Session manager → client/server subsystem → logon → user init chain.
	"smss.exe", "csrss.exe", "winlogon.exe", "userinit.exe",
	// The service control manager, the security authority, and the generic host.
	"services.exe", "lsass.exe", "svchost.exe",
	// Boot-time check/repair and the command shell the base boots to.
	"autochk.exe", "cmd.exe",
	// Console utilities the base ships as usable commands; each is its own load
	// root, so its imports are covered by the closure rather than hand-listed.
	"chcp.com", "more.com", "find.exe", "findstr.exe", "fc.exe", "xcopy.exe",
	"print.exe", "shutdown.exe",
}

// ClosureActions computes the base file set's import-derivable half: the static import
// closure of the load roots plus the source's own boot drivers. The roots themselves are
// placed by the base manifest and driver derivation (which own their renames and target
// directories), so only their transitive dependencies are emitted. It delegates to the
// shared plugin.ClosureActions engine — the same computation a plugin's [Closure] section
// drives — differing only in how the seeds are gathered.
func ClosureActions(pw *pewalk.Source, l *layout.Layout) ([]action.Action, []string, error) {
	seeds := append([]string(nil), closureSeed...)
	seeds = append(seeds, l.BootDrivers()...)
	// The roots are placed by the base manifest and driver derivation, so the closure
	// emits only their dependencies.
	return plugin.ClosureActions(pw, seeds, false)
}
