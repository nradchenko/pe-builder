// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/internal/base"
	"github.com/nradchenko/pe-builder/internal/tree"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// coreMemberPaths resolves the pre-wimmf boot closure (base.ComputeBootCore) to the set of
// output-tree-relative paths that make up coreimg — the bridge between the closure, which
// names members by their *source* name, and the assembled tree, which holds them at their
// *placed* path (after renames like ntkrnlmp.exe → ntoskrnl.exe).
//
// Each Staged name is matched against the copy actions and mapped to where the tree placed it
// (tree.OutputPath, the same resolution Assemble used). Only paths the tree actually wrote are
// kept: a core member the source could not supply — a skipped optional driver, or a Staged name
// no action places — is dropped here rather than failing the coreimg master, mirroring how the
// base layer treats optional copies. (A boot-start driver genuinely missing everywhere is a
// base-layer/superset concern surfaced there, not a coreimg one — it is equally absent from the
// WIM.)
//
// That drop is deliberate but blunt: because Staged is an undifferentiated superset (every
// *.Load class, so it carries optional drivers the source may lack), this cannot tell a
// genuinely required boot-start driver from an optional one, so it tolerates every unplaced
// member — which is why internal/coreimg.stageMembers' hard-error on a missing member never
// fires from this path. requireCoreEssentials backstops the always-required subset (the loader
// chain, kernel, a HAL, the fltmgr/wimmf/firadisk overlay, the SYSTEM hive) so their omission
// fails the build instead of booting to STOP 0x7B. Failing on *any* missing required member —
// not just that fixed subset — needs the built hive's Start=0 values to separate required from
// optional (a separate change).
//
// A Staged name can be either end of a rename, so both are matched. The source's own renames
// run one way — the closure names ntkrnlmp.exe and the tree places it as ntoskrnl.exe — but a
// plugin staging an architecture-scoped binary runs the other: it ships firadisk-amd64.sys and
// renames it to firadisk.sys, which is the name the boot-driver wiring (and so the closure)
// refers to. Matching only the source name drops exactly those drivers from the core while
// leaving them in the tree, so the image assembles and setupldr then fails to find a boot-start
// driver it was told to load. Since every plugin shipping both architectures must rename at
// staging, that is the normal case on amd64, not an edge one.
//
// The generated members (base.ComputeBootCore's Generated — the SYSTEM hive) are produced by
// a later stage than tree assembly, so they are not in written; each is resolved by base name
// among the hive stage's own output paths. The disc-identity markers (WIN51*, the boot font)
// are added too: they sit at the tree root, outside both the closure and the written set, but
// setupldr validates the coreimg as a Windows disc and renders its boot text from the font, so
// a coreimg without them prompts for the CD (and garbles a localized boot screen).
//
// The result is the members argument for internal/coreimg.Build: sorted, output-tree-relative,
// forward-slash paths, each an existing file under the assembled tree.
func coreMemberPaths(core base.BootCore, acts []action.Action, l *layout.Layout, roots source.Roots, written, hivePaths, markers []string) []string {
	staged := make(map[string]bool, len(core.Staged))
	for _, s := range core.Staged {
		staged[s] = true // ComputeBootCore lowercases Staged
	}
	writtenSet := make(map[string]bool, len(written))
	for _, w := range written {
		writtenSet[w] = true
	}

	out := map[string]bool{}
	for _, a := range acts {
		if a.Kind != action.CopyFile {
			continue
		}
		p := tree.OutputPath(a, l, roots)
		if !staged[strings.ToLower(a.Src)] && !staged[strings.ToLower(path.Base(p))] {
			continue
		}
		if writtenSet[p] {
			out[p] = true
		}
	}
	// Generated members come from a stage other than tree assembly (the SYSTEM hive from the
	// hive stage), so they are absent from written; resolve each from the paths its stage
	// reports, matched by base name.
	for _, g := range core.Generated {
		for _, h := range hivePaths {
			if strings.EqualFold(path.Base(h), g) {
				out[h] = true
			}
		}
	}
	// The disc-identity markers and boot font, placed at the tree root by the marker stage.
	for _, mk := range markers {
		out[mk] = true
	}
	// Text-mode setup and early kernel init load the NLS code-page/sort tables and the OEM
	// console font from system32 before wimmf serves anything — non-PE files, so outside the
	// import closure. Include every .nls and console .fon the tree placed under system32
	// (the GDI/UI fonts go to the Fonts directory, not here), so the coreimg — the disc
	// setupldr reads — carries them; without the OEM font setupldr stops on the missing file,
	// and without the code-page tables the kernel cannot build its locale.
	for _, w := range written {
		if !strings.HasSuffix(strings.ToLower(path.Dir(w)), "/system32") {
			continue
		}
		if b := strings.ToLower(path.Base(w)); strings.HasSuffix(b, ".nls") || strings.HasSuffix(b, ".fon") {
			out[w] = true
		}
	}

	res := make([]string, 0, len(out))
	for p := range out {
		res = append(res, p)
	}
	sort.Strings(res)
	return res
}

// coreEssentials are the boot-core members (by lower-case base name) whose absence guarantees an
// unbootable image: the real-mode loader chain, the kernel, the fltmgr/wimmf/firadisk overlay,
// and the SYSTEM hive. A HAL is required too but its name varies by architecture, so it is
// checked separately (any hal*.dll). This is not the full required set — the exotic boot drivers
// in the Staged superset are optional per-hardware — only the members that are always mandatory.
var coreEssentials = []string{
	"setupldr.bin", "ntdetect.com", "biosinfo.inf", "txtsetup.sif", // real-mode loader chain
	"ntoskrnl.exe",                            // kernel (placed from ntkrnlmp.exe)
	"fltmgr.sys", "wimmf.sys", "firadisk.sys", // the overlay chain
	"setupreg.hiv", // the SYSTEM hive
}

// requireCoreEssentials fails when the resolved boot core is missing an always-mandatory member.
// coreMemberPaths drops any Staged member the tree did not place (the superset carries optional
// drivers), which by itself lets a genuinely fatal omission through silently — an arch-scoped
// plugin binary that never landed in the coreimg has done exactly this (wimmf.sys/firadisk.sys
// dropped on amd64), producing a build that masters cleanly and then boots to STOP 0x7B. This
// guard turns that class of omission into a build-time error. It checks only the fixed
// always-required set (see coreEssentials) plus a HAL; the broader "every required boot-start
// driver" check awaits the built hive's Start=0 values.
func requireCoreEssentials(members []string) error {
	present := make(map[string]bool, len(members))
	for _, m := range members {
		present[strings.ToLower(path.Base(m))] = true
	}
	var missing []string
	for _, name := range coreEssentials {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	hasHAL := false
	for b := range present {
		if strings.HasPrefix(b, "hal") && strings.HasSuffix(b, ".dll") {
			hasHAL = true
			break
		}
	}
	if !hasHAL {
		missing = append(missing, "a hal*.dll")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("boot core missing required member(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
