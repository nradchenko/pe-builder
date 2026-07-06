// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// fixtureLayout provides just enough of a source layout to derive boot drivers.
func fixtureLayout() *layout.Layout {
	return &layout.Layout{
		Dirs: map[string]string{"2": "system32", "4": "system32/drivers"},
		Files: map[string]layout.FileEntry{
			"atapi.sys": {Name: "atapi.sys", TargetDir: "4"},
			"ntfs.sys":  {Name: "ntfs.sys", TargetDir: "4"},
			"hal.dll":   {Name: "hal.dll", TargetDir: "2"},
		},
		Load: map[string][]string{
			"Hal":         {"hal.dll", "halacpi.dll"},
			"FileSystems": {"ntfs.sys", "fastfat.sys"},
			"SCSI":        {"atapi.sys"},
		},
	}
}

func TestDriverActions(t *testing.T) {
	got := driverActions(fixtureLayout())
	want := []action.Action{
		{Kind: action.CopyFile, Src: "hal.dll", Optional: true},
		{Kind: action.CopyFile, Src: "halacpi.dll", Optional: true},
		{Kind: action.CopyFile, Src: "ntfs.sys", Optional: true},
		{Kind: action.CopyFile, Src: "fastfat.sys", Optional: true},
		{Kind: action.CopyFile, Src: "atapi.sys", Optional: true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("driverActions mismatch (-want +got):\n%s", diff)
	}
}

// TestActions compiles the embedded base.inf plus fixture-derived drivers and diffs
// the whole action set against a golden rendering, so a change to base.inf is a
// visible, reviewable diff.
func TestActions(t *testing.T) {
	acts, err := Actions(fixtureLayout(), source.Identity{Build: 2600, Arch: "i386"})
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "base-actions", renderActions(acts))
}

// renderActions serializes an action list to a stable, readable form for golden
// diffing: one line per action with only the fields relevant to its kind.
func renderActions(acts []action.Action) string {
	var b strings.Builder
	for _, a := range acts {
		switch a.Kind {
		case action.CopyFile:
			fmt.Fprintf(&b, "copy %s dir=%q rename=%q flags=%s\n",
				a.Src, a.DirID, a.Rename, copyFlags(a))
		case action.Mkdir:
			fmt.Fprintf(&b, "mkdir id=%s path=%q fromRoot=%v always=%v\n",
				a.DirID, a.Path, a.FromRoot, a.AlwaysCreate)
		case action.AddReg:
			fmt.Fprintf(&b, "addreg hive=%s type=0x%x subkey=%q value=%q data=%q\n",
				a.Hive, a.RegType, a.Subkey, a.ValueName, a.Data)
		case action.DelReg:
			fmt.Fprintf(&b, "delreg hive=%s subkey=%q value=%q\n", a.Hive, a.Subkey, a.ValueName)
		case action.SetValue:
			fmt.Fprintf(&b, "setvalue file=%q section=%q key=%q data=%q\n",
				a.TargetFile, a.TargetSection, a.LineKey, a.LineData)
		default:
			fmt.Fprintf(&b, "%s\n", a.Kind)
		}
	}
	return b.String()
}

func copyFlags(a action.Action) string {
	var f []string
	if a.NoDecompress {
		f = append(f, "nodecompress")
	}
	if a.Optional {
		f = append(f, "optional")
	}
	if a.Uppercase {
		f = append(f, "uppercase")
	}
	if a.ShipWithPlugin {
		f = append(f, "shipwithplugin")
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, "|")
}
