// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// SourceDisksFiles attribute bits (see docs/reference/03-plugin-format.md).
const (
	attrShipWithPlugin = 1 // file ships with the plugin; must be present
	attrNoDecompress   = 2 // copy the compressed archive as-is
	attrOptional       = 4 // copy only if present
	attrUppercase      = 8 // rename the target to uppercase
)

// WinntDirectories attribute bits.
const (
	dirAlwaysCreate = 1 // create even when empty
	dirFromRoot     = 2 // create from the output root, not inside \i386
)

// Actions compiles a plugin's copy/registry/text sections into the action IR for
// the given build. Sections are visited in a fixed order (directories, file
// copies, registry edits, then text patches); within a section, file order is
// preserved. .BuildNo variants are folded in by inf.File.Section.
func Actions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	add := func(as []action.Action, err error) error {
		if err != nil {
			return err
		}
		out = append(out, as...)
		return nil
	}

	if err := add(dirsActions(f, build)); err != nil {
		return nil, err
	}
	if err := add(copyActions(f, build)); err != nil {
		return nil, err
	}
	for _, r := range []struct {
		section string
		hive    action.RegHive
	}{
		{"Software", action.HiveSoftware},
		{"SetupReg", action.HiveSystem},
		{"Default", action.HiveDefault},
	} {
		if err := add(regActions(f, build, r.section, r.hive)); err != nil {
			return nil, err
		}
	}
	if err := add(setValueActions(f, build)); err != nil {
		return nil, err
	}
	if err := add(lineActions(f, build, "AddLine", action.AddLine)); err != nil {
		return nil, err
	}
	if err := add(lineActions(f, build, "DelLine", action.DelLine)); err != nil {
		return nil, err
	}
	if err := add(appendActions(f, build)); err != nil {
		return nil, err
	}
	return out, nil
}

// dirsActions maps [WinntDirectories]: directoryID=dirname[, attribute].
func dirsActions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("WinntDirectories", build) {
		attr, _ := l.IntField(1)
		out = append(out, action.Action{
			Kind:         action.Mkdir,
			DirID:        l.Key,
			Path:         l.Field(0),
			AlwaysCreate: attr&dirAlwaysCreate != 0,
			FromRoot:     attr&dirFromRoot != 0,
		})
	}
	return out, nil
}

// copyActions maps [SourceDisksFiles]: filename=directoryID[, renamed][, attribute].
func copyActions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("SourceDisksFiles", build) {
		attr, _ := l.IntField(2)
		out = append(out, action.Action{
			Kind:           action.CopyFile,
			Src:            l.Key,
			DirID:          l.Field(0),
			Rename:         l.Field(1),
			ShipWithPlugin: attr&attrShipWithPlugin != 0,
			NoDecompress:   attr&attrNoDecompress != 0,
			Optional:       attr&attrOptional != 0,
			Uppercase:      attr&attrUppercase != 0,
		})
	}
	return out, nil
}

// regActions maps [<prefix>.AddReg]/[<prefix>.DelReg] to the given hive.
func regActions(f *inf.File, build int, prefix string, hive action.RegHive) ([]action.Action, error) {
	var out []action.Action

	for _, l := range f.Section(prefix+".AddReg", build) {
		// type,"subkey","valuename",data...
		regType, ok := l.IntField(0)
		if !ok {
			return nil, fmt.Errorf("[%s.AddReg]: line %q has no registry type", prefix, l.Raw())
		}
		a := action.Action{
			Kind:      action.AddReg,
			Hive:      hive,
			Subkey:    l.Field(1),
			ValueName: l.Field(2),
			RegType:   int(regType),
		}
		switch int(regType) {
		case action.RegBinary:
			data, ok := l.BinaryFields(3)
			if !ok {
				return nil, fmt.Errorf("[%s.AddReg]: line %q has malformed binary data", prefix, l.Raw())
			}
			a.DataBytes = data
		case action.RegMultiSZ:
			// A REG_MULTI_SZ value is written as several comma-separated strings;
			// join them with NUL, the on-disk multi-string separator.
			if fields := l.Fields(); len(fields) > 3 {
				a.Data = strings.Join(fields[3:], "\x00")
			}
		default:
			a.Data = l.Field(3)
		}
		out = append(out, a)
	}

	for _, l := range f.Section(prefix+".DelReg", build) {
		// "subkey"[,"valuename"]
		out = append(out, action.Action{
			Kind:      action.DelReg,
			Hive:      hive,
			Subkey:    l.Field(0),
			ValueName: l.Field(1),
		})
	}
	return out, nil
}

// setValueActions maps [SetValue]: "file","section","key"[,"value"].
func setValueActions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("SetValue", build) {
		out = append(out, action.Action{
			Kind:          action.SetValue,
			TargetFile:    l.Field(0),
			TargetSection: l.Field(1),
			LineKey:       l.Field(2),
			LineData:      l.Field(3),
		})
	}
	return out, nil
}

// lineActions maps [AddLine]/[DelLine]: "file","section","line".
func lineActions(f *inf.File, build int, section string, kind action.Kind) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section(section, build) {
		out = append(out, action.Action{
			Kind:          kind,
			TargetFile:    l.Field(0),
			TargetSection: l.Field(1),
			LineData:      l.Field(2),
		})
	}
	return out, nil
}

// appendActions maps [Append]: Filename, FileNameToAppend.
func appendActions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("Append", build) {
		// [Append] lines are keyless (Filename, FileNameToAppend). Some may be
		// written key=value (Filename=FileNameToAppend); handle both.
		target, appended := l.Field(0), l.Field(1)
		if l.Key != "" {
			target, appended = l.Key, l.Field(0)
		}
		out = append(out, action.Action{
			Kind:       action.Append,
			TargetFile: target,
			AppendFile: appended,
		})
	}
	return out, nil
}
