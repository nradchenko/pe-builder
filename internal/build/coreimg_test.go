// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/internal/base"
	"github.com/nradchenko/pe-builder/source"
)

// TestRequireCoreEssentials checks the backstop that fails a build when the coreimg is missing an
// always-mandatory member: a complete core passes, and dropping either the overlay driver (the
// silent amd64 rename bug this guards against) or the HAL is rejected.
func TestRequireCoreEssentials(t *testing.T) {
	full := []string{
		"i386/setupldr.bin", "i386/ntdetect.com", "i386/biosinfo.inf", "i386/txtsetup.sif",
		"i386/system32/ntoskrnl.exe", "i386/system32/halaacpi.dll",
		"i386/system32/drivers/fltmgr.sys", "i386/system32/drivers/wimmf.sys",
		"i386/system32/drivers/firadisk.sys", "i386/system32/SETUPREG.HIV",
	}
	if err := requireCoreEssentials(full); err != nil {
		t.Errorf("complete core rejected: %v", err)
	}

	drop := func(suffix string) []string {
		var out []string
		for _, m := range full {
			if strings.HasSuffix(strings.ToLower(m), suffix) {
				continue
			}
			out = append(out, m)
		}
		return out
	}
	if err := requireCoreEssentials(drop("/wimmf.sys")); err == nil {
		t.Error("core missing wimmf.sys accepted; want an error")
	}
	if err := requireCoreEssentials(drop("halaacpi.dll")); err == nil {
		t.Error("core missing a HAL accepted; want an error")
	}
}

// TestCoreMemberPaths checks the closure→tree bridge: a Staged source name is mapped to its
// placed path, non-core files are excluded, a core name nothing placed (or the tree skipped)
// is dropped, and the generated SYSTEM hive is added from the hive stage's outputs.
//
// The actions carry an explicit Dest, so tree.OutputPath returns it verbatim without needing
// a real layout — the rename (ntkrnlmp.exe → ntoskrnl.exe) is modeled by the Dest, and the
// bridge's own selection logic is what is under test.
func TestCoreMemberPaths(t *testing.T) {
	acts := []action.Action{
		// Core: the kernel, placed under its renamed output name.
		{Kind: action.CopyFile, Src: "ntkrnlmp.exe", Dest: "i386/ntoskrnl.exe"},
		// Core: a boot driver and a plugin-staged WIM-chain driver.
		{Kind: action.CopyFile, Src: "atapi.sys", Dest: "i386/system32/drivers/atapi.sys"},
		{Kind: action.CopyFile, Src: "wimmf.sys", Dest: "i386/system32/drivers/wimmf.sys"},
		// Not core: belongs only in the WIM.
		{Kind: action.CopyFile, Src: "shell32.dll", Dest: "i386/system32/shell32.dll"},
		// Core name, but the tree never wrote it (optional driver absent from the source):
		// must be dropped, not carried into coreimg (which would then fail to master it).
		{Kind: action.CopyFile, Src: "exotic.sys", Dest: "i386/system32/drivers/exotic.sys"},
		// A non-copy action is ignored.
		{Kind: action.Mkdir, Dest: "i386/system32/drivers"},
	}
	core := base.BootCore{
		Staged:    []string{"ntkrnlmp.exe", "atapi.sys", "wimmf.sys", "exotic.sys"},
		Generated: []string{"setupreg.hiv"},
	}
	// exotic.sys is absent from written; the hive lives among the hive stage's outputs.
	// The NLS table and OEM console font under system32 are pulled in by the text-setup rule
	// (not the closure); the GDI font under Fonts/ and shell32.dll must stay out.
	written := []string{
		"i386/ntoskrnl.exe",
		"i386/system32/drivers/atapi.sys",
		"i386/system32/drivers/wimmf.sys",
		"i386/system32/shell32.dll",
		"i386/system32/c_866.nls",
		"i386/system32/vga866.fon",
		"i386/Fonts/vgasys.fon",
	}
	hivePaths := []string{"i386/system32/SETUPREG.HIV", "i386/system32/config/SOFTWARE"}
	// Disc-identity markers and boot font at the tree root; the coreimg must carry them or
	// setupldr prompts for the CD (and garbles a localized boot screen).
	markers := []string{"WIN51", "WIN51IP", "WIN51IP.SP3", "bootfont.bin"}

	got := coreMemberPaths(core, acts, nil, source.Roots{}, written, hivePaths, markers)
	want := []string{
		"WIN51",
		"WIN51IP",
		"WIN51IP.SP3",
		"bootfont.bin",
		"i386/ntoskrnl.exe",
		"i386/system32/SETUPREG.HIV",
		"i386/system32/c_866.nls",
		"i386/system32/drivers/atapi.sys",
		"i386/system32/drivers/wimmf.sys",
		"i386/system32/vga866.fon",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("coreMemberPaths\n got: %q\nwant: %q", got, want)
	}
}
