// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/plugin"
)

// BootCore is the pre-wimmf boot closure: the exact set of files that must be
// physically present in the boot core before the wimmf overlay filter can serve the
// rest of the tree out of the WIM. It is what the WIM-boot media tail masters into a
// RAM-booted image ahead of the compressed WIM.
//
// The membership rule is precise in both directions. A boot-start (Start=0) file that
// is missing is fatal — the kernel needs it before wimmf exists to fabricate it, so its
// absence is INACCESSIBLE_BOOT_DEVICE (STOP 0x7B), not a late failure. Anything not on
// this list belongs in the WIM, where wimmf serves it on demand; over-including only
// wastes core size (and boot RAM).
//
// Members come from three provenances, kept separate because a later stage resolves each
// differently (a source file is decompressed and placed from the media; a plugin driver
// is staged from its package; a generated artifact is written by a build stage):
//
//   - Staged   — files staged into the tree: the source import closure, the source's
//     boot-start drivers and filter manager, and the WIM-boot chain's own driver
//     binaries (from plugins). Lowercased names.
//   - Generated — files a build stage produces rather than copies: the SYSTEM registry
//     hive (setupreg.hiv). Named as the build writes it.
//   - Missing   — seed/import names the walk could not resolve against the source and
//     that are not otherwise core members, reported for diagnostics only. A name that is
//     an explicit member (a boot driver, a WIM-chain driver) is never reported here even
//     if the walk could not resolve it, so Missing and Staged never overlap. Typical
//     entries are import names a source satisfies under a different file: the kernel
//     imports hal.dll, and on media that do not ship a literal hal.dll the concrete HAL
//     (halacpi.dll, …) enters the core through the boot-driver list under its real name
//     while hal.dll shows up here. (Media that do ship a literal hal.dll resolve it, so it
//     lands in Staged instead — it is source-dependent, not fixed.)
type BootCore struct {
	Staged    []string
	Generated []string
	Missing   []string
}

// bootCoreSeed are the source-resolvable load roots whose static import closure must be
// present before wimmf serves the WIM. Unlike the base layer's closureSeed — which spans
// the whole PE — this stops at the boot phase: no session manager / client-server / logon
// chain, all of which load from the WIM after wimmf is up.
//
// Both kernel names are roots for the same reason closure.go lists them: the shipped
// kernel is the multiprocessor ntkrnlmp.exe (placed as ntoskrnl.exe), and naming
// ntoskrnl.exe as a root keeps the closure from also pulling the source's separate
// uniprocessor kernel of that name (which drivers reach as an import). fltmgr.sys is the
// filter manager the wimmf overlay is a client of; it loads boot-start ahead of the boot
// volume, so it is core. The boot-start drivers themselves are added at scan time from the
// source's txtsetup.sif *.Load lists (a property of the source), which pulls their
// port/class-library imports into the closure too.
var bootCoreSeed = []string{"ntkrnlmp.exe", "ntoskrnl.exe", "fltmgr.sys"}

// wimBootCoreDrivers are the WIM-boot chain's own boot-start drivers: firadisk reclaims
// the RAM region the pre-loader mapped, and wimmf overlays the WIM. Both are Start=0 and
// so must be in the core, but neither is on the Windows source — they are staged by the
// WIM-boot infrastructure plugins — so the import walk cannot resolve them and they are
// named here. Their own imports (fltmgr, ntoskrnl, hal) are already covered by the source
// closure.
var wimBootCoreDrivers = []string{"firadisk.sys", "wimmf.sys"}

// loaderFiles are the real-mode loader chain: the NT setup loader, hardware detector, its
// BIOS-info INF, and the text-setup manifest. They live on the source but an import walk
// cannot reach them (setupldr.bin is a real-mode image, not a PE with an import table), so
// they are named as fixed core members.
var loaderFiles = []string{"setupldr.bin", "ntdetect.com", "biosinfo.inf", "txtsetup.sif"}

// baseSeedBootFiles are boot-time drivers the base layer seeds that no import scan or
// txtsetup.sif *.Load list reaches, yet setupldr (and its text-setup mini-driver) load off
// the boot volume before the wimmf overlay is serving, so they must be physically in the
// coreimg: the setup mini-driver and its language driver (setupdd.sys, spddlang.sys), the
// keyboard class driver setupldr loads for text-setup input (kbdclass.sys), the NDIS wrapper
// setup's detection pass loads (ndis.sys), and the boot-start Multiple UNC Provider (mup.sys).
//
// The criterion is "setupldr loads it off the boot media before the kernel", NOT the registry
// Start value — kbdclass and ndis are Start=1 in the hive, but setupldr's text-setup pass
// loads them regardless (each surfaced as a missing-file stop on the text loader screen). Base
// seeds that load genuinely later stay in the WIM: mouclass is already in the source's
// MouseDrivers.Load; null/npfs/beep/fs_rec are Start=1 loaded by the SCM in Phase 1; afd is the
// LSA/RPC TDI driver; dxg/win32k are the runtime graphics stack — all after wimmf attaches. The
// CDDB-bound processor driver (processr.sys) is in that later group too: a boot trace shows it
// loads after the overlay goes live, so wimmf serves it from the WIM — it was over-included here
// while the chain was being brought up and has been removed.
//
// This is a hand-list mirroring base.inf's boot seed, which drifts if that seed changes;
// modelling what setupldr loads off the boot media (rather than Start=0 alone) is the durable
// replacement (tracked as a follow-up).
var baseSeedBootFiles = []string{"setupdd.sys", "spddlang.sys", "kbdclass.sys", "ndis.sys", "mup.sys"}

// generatedCoreFiles are the core members a build stage produces rather than copies from a
// source or plugin: the SYSTEM registry hive the /minint loader reads (build/hives.go writes
// it to the tree as SETUPREG.HIV). It is an unconditional core member, named as the build
// writes it rather than derived, because it does not exist until the hive stage creates it.
var generatedCoreFiles = []string{"setupreg.hiv"}

// ComputeBootCore computes the pre-wimmf boot closure for a probed source. It walks the
// static import closure of the kernel, the source's boot-start drivers, and the filter
// manager (emitting only their dependencies — the seeds and drivers are added as members
// directly), then unions that with the boot-start drivers themselves, the WIM-boot chain
// drivers, the real-mode loader files, and the generated artifacts.
//
// It is a calculation: it returns the member set (and the diagnostic red herrings) so the
// set is verifiable — via a golden test — without booting. Mastering the members into a
// bootable image is a separate media-tail step, which owns where on the media each member
// lands and how each is materialized (decompressed from the source, staged from a plugin,
// or taken from a build stage's output).
// pinnedHAL is the HAL file name the loader maps — base.inf's osloadoptions /hal= value,
// which the caller extracts from the compiled actions with PinnedHAL. The source ships several
// HAL variants and the kernel's hal.dll import pulls them all into the closure; the core keeps
// this one and drops the rest. Empty keeps them all (never delete the loader's HAL on a guess).
func ComputeBootCore(pw *pewalk.Source, l *layout.Layout, pinnedHAL string) (BootCore, error) {
	seeds := append([]string(nil), bootCoreSeed...)
	seeds = append(seeds, l.BootDrivers()...)

	// emitSeeds=false: the seeds (kernel, fltmgr) and the boot drivers are added as
	// members explicitly below, so the walk contributes only their transitive
	// dependencies — the low-level modules (bootvid.dll, kdcom.dll) and the port/class
	// libraries the boot drivers import.
	deps, redHerrings, err := plugin.ClosureActions(pw, seeds, false)
	if err != nil {
		return BootCore{}, err
	}

	staged := map[string]bool{}
	add := func(name string) {
		if n := strings.ToLower(strings.TrimSpace(name)); n != "" {
			staged[n] = true
		}
	}
	// The shipped multiprocessor kernel and the filter manager (both suppressed from the
	// walk output as seeds), the source's boot-start drivers, the WIM-chain drivers, and
	// the real-mode loader files.
	add("ntkrnlmp.exe")
	add("fltmgr.sys")
	for _, d := range l.BootDrivers() {
		add(d)
	}
	for _, d := range wimBootCoreDrivers {
		add(d)
	}
	for _, f := range loaderFiles {
		add(f)
	}
	for _, f := range baseSeedBootFiles {
		add(f)
	}
	// The computed transitive dependencies.
	for _, a := range deps {
		if a.Kind == action.CopyFile {
			add(a.Src)
		}
	}

	// Keep only the one HAL the loader actually maps. The source ships several HAL variants and
	// the kernel's hal.dll import pulls them all into the closure, but the loader maps exactly
	// the one base.inf's osloadoptions names (/hal=…) — a boot trace shows the others never
	// load. Drop the other hal*.dll. Using the caller-supplied pin (from base.inf, x86 pins
	// halaacpi.dll, amd64 hal.dll) keeps this in step with what the loader is told to map; if no
	// HAL is named, or the pinned one is not staged, keep them all rather than guess one away.
	pinned := strings.ToLower(pinnedHAL)
	if pinned != "" && staged[pinned] {
		for name := range staged {
			if name != pinned && strings.HasPrefix(name, "hal") && strings.HasSuffix(name, ".dll") {
				delete(staged, name)
			}
		}
	}

	// Report as missing only the unresolved names that are not otherwise core members:
	// a boot driver or WIM-chain driver the walk could not resolve is still staged (added
	// explicitly above), so it is not a gap. This keeps Missing and Staged disjoint.
	var missing []string
	for _, name := range redHerrings {
		if n := strings.ToLower(strings.TrimSpace(name)); n != "" && !staged[n] {
			missing = append(missing, n)
		}
	}

	return BootCore{
		Staged:    sortedKeys(staged),
		Generated: append([]string(nil), generatedCoreFiles...),
		Missing:   sortedUnique(missing),
	}, nil
}

// PinnedHAL returns the HAL file name the loader maps, read from the compiled actions — the
// /hal= token of the txtsetup.sif SetupData\osloadoptions edit the base layer emits — lowercased,
// or "" if none is named. Taking the already-compiled actions (rather than re-parsing base.inf)
// keeps the arch-scoped [SetValue.<arch>] resolution and avoids compiling base.inf twice; pass
// the result to ComputeBootCore.
func PinnedHAL(acts []action.Action) string {
	for _, a := range acts {
		if a.Kind == action.SetValue &&
			strings.EqualFold(a.TargetFile, "txtsetup.sif") &&
			strings.EqualFold(a.TargetSection, "SetupData") &&
			strings.EqualFold(a.LineKey, "osloadoptions") {
			return halFromOsloadoptions(a.LineData)
		}
	}
	return ""
}

// halFromOsloadoptions extracts the /hal= value from an osloadoptions string, lowercased and
// with any surrounding quotes removed (the value is stored quoted for txtsetup.sif). It returns
// "" if the string names no HAL.
func halFromOsloadoptions(opts string) string {
	opts = strings.ReplaceAll(opts, "\"", "")
	for _, tok := range strings.Fields(opts) {
		if v, ok := strings.CutPrefix(strings.ToLower(tok), "/hal="); ok {
			return v
		}
	}
	return ""
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
