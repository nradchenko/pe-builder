// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/golden"
)

// TestComputeBootCore is the durable regression guard for the pre-wimmf boot closure:
// the set of files that must be physically present before the wimmf overlay filter can
// serve the rest from the WIM. It runs against a real source (the closure's dependency
// set is a property of a real medium's import graph) and pins the computed Staged set
// with a golden fixture — a change to what the boot drivers or kernel pull in shows up as
// a diff. Regenerate the fixture with `go test -update` after an intended change.
func TestComputeBootCore(t *testing.T) {
	_, id, l, pw := realSource(t)

	// The pinned HAL comes from base.inf's osloadoptions, as in a real build; the seed-only
	// action set (pw=nil, no closure walk) is enough to carry that edit.
	seed, err := Actions(nil, l, id)
	if err != nil {
		t.Fatalf("compile base seed: %v", err)
	}
	core, err := ComputeBootCore(pw, l, PinnedHAL(seed))
	if err != nil {
		t.Fatalf("compute boot core: %v", err)
	}

	golden.Assert(t, "boot-core-staged", strings.Join(core.Staged, "\n")+"\n")

	has := func(list []string, name string) bool {
		for _, s := range list {
			if s == name {
				return true
			}
		}
		return false
	}

	// The only generated artifact is the SYSTEM hive, named as the build writes it. X is
	// deliberately excluded (not build-generated, not in the payload tree, unused by this chain).
	if got := strings.Join(core.Generated, ","); got != "setupreg.hiv" {
		t.Errorf("Generated = %q, want \"setupreg.hiv\"", got)
	}

	// The load-bearing invariants, asserted by name so an accidental drop is caught even
	// if the golden is regenerated over it. Each of these missing is STOP 0x7B.
	for _, name := range []string{
		"ntkrnlmp.exe",              // the shipped multiprocessor kernel
		"fltmgr.sys",                // filter manager the overlay depends on
		"firadisk.sys", "wimmf.sys", // the WIM-boot chain's own boot-start drivers
		"setupldr.bin", "ntdetect.com", "biosinfo.inf", "txtsetup.sif", // real-mode loader
		"setupdd.sys", "spddlang.sys", "mup.sys", // base-seeded pre-wimmf boot drivers
		"bootvid.dll", "kdcom.dll", // import-derived kernel dependencies
	} {
		if !has(core.Staged, name) {
			t.Errorf("boot core missing required member %q", name)
		}
	}

	// The source's separate uniprocessor kernel must NOT be a member: the multiprocessor
	// kernel is shipped as ntoskrnl.exe, and pulling the uniprocessor file of that name
	// would place a second, wrong kernel. It is a seed only to suppress it from the walk.
	if has(core.Staged, "ntoskrnl.exe") {
		t.Errorf("boot core includes the uniprocessor ntoskrnl.exe; only ntkrnlmp.exe should be a member")
	}

	// Missing is diagnostics-only and must never overlap Staged: a name the walk could not
	// resolve but that is an explicit member (a boot driver, a WIM-chain driver) is still a
	// core member, not a gap, so it must not also be reported missing. This invariant holds
	// regardless of what the source happens to ship.
	stagedSet := make(map[string]bool, len(core.Staged))
	for _, s := range core.Staged {
		stagedSet[s] = true
	}
	for _, m := range core.Missing {
		if stagedSet[m] {
			t.Errorf("%q reported in both Staged and Missing; the two must be disjoint", m)
		}
	}
}
