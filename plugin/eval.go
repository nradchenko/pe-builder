// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/source"
)

// SourceDisksFiles attribute bits. 1–8 are the original format's
// (see docs/reference/03-plugin-format.md); 16 is a pebuild extension.
const (
	attrShipWithPlugin = 1 // file ships with the plugin; must be present
	attrNoDecompress   = 2 // copy the compressed archive as-is
	attrOptional       = 4 // copy only if present
	attrUppercase      = 8 // rename the target to uppercase
	// attrBootArea marks a file as part of the real-mode boot chain, which lives in a
	// different source folder from the OS payload on amd64 media. It extends the
	// original format, which defines no bit above 8: a plugin written for the original
	// tool never sets it and so is read unchanged, while one that does set it is not
	// portable back — the original tool builds no amd64 source for it to matter on.
	attrBootArea = 16
)

// WinntDirectories attribute bits. 1 and 2 are the original format's; 16 is the
// same pebuild extension as attrBootArea, kept at the same value so one number means
// "boot chain" in both sections.
const (
	dirAlwaysCreate = 1 // create even when empty
	dirFromRoot     = 2 // create at the output root, not under the area's folder
	dirBootArea     = 16
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
	// A copy may target a directory the plugin defines itself in [WinntDirectories]
	// (a letter or number ID), not one the Windows source's txtsetup.sif knows, so it
	// is resolved against the plugin's own table here rather than the source layout.
	if err := add(copyActions(f, build, winntDirs(f, build))); err != nil {
		return nil, err
	}
	if err := add(sourcePathActions(f, build)); err != nil {
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
			Area:         bootAreaAttr(attr, dirBootArea),
		})
	}
	return out, nil
}

// dirTarget is a directory a plugin defines in [WinntDirectories]: its path, whether
// it is created at the output root rather than under its area's folder, and which
// area that is.
type dirTarget struct {
	path     string
	fromRoot bool
	area     source.Area
}

// winntDirs indexes the plugin's [WinntDirectories] by directory ID so a copy can
// resolve a directory the plugin defines itself.
func winntDirs(f *inf.File, build int) map[string]dirTarget {
	dirs := make(map[string]dirTarget)
	for _, l := range f.Section("WinntDirectories", build) {
		attr, _ := l.IntField(1)
		dirs[l.Key] = dirTarget{
			path:     l.Field(0),
			fromRoot: attr&dirFromRoot != 0,
			area:     bootAreaAttr(attr, dirBootArea),
		}
	}
	return dirs
}

// bootAreaAttr reads the source area from an attribute field, given the section's
// boot-chain bit. Everything is OS payload unless explicitly flagged otherwise, so an
// INF that says nothing — every plugin written before the bit existed — keeps its
// files and directories where they were.
func bootAreaAttr(attr int64, bootBit int64) source.Area {
	if attr&bootBit != 0 {
		return source.AreaBoot
	}
	return source.AreaSystem
}

// copyActions maps [SourceDisksFiles]: filename=directoryID[, renamed][, attribute].
// A directory ID the plugin defines itself (in dirs) is resolved to its path here, so
// the copy lands there rather than being looked up — and missed — in the source
// layout. IDs the plugin does not define (the predefined source IDs like 2=system32)
// are left for the tree assembler to resolve against the source layout.
func copyActions(f *inf.File, build int, dirs map[string]dirTarget) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("SourceDisksFiles", build) {
		attr, _ := l.IntField(2)
		a := action.Action{
			Kind:           action.CopyFile,
			Src:            l.Key,
			DirID:          l.Field(0),
			Rename:         l.Field(1),
			ShipWithPlugin: attr&attrShipWithPlugin != 0,
			NoDecompress:   attr&attrNoDecompress != 0,
			Optional:       attr&attrOptional != 0,
			Uppercase:      attr&attrUppercase != 0,
			Area:           bootAreaAttr(attr, attrBootArea),
		}
		if d, ok := dirs[a.DirID]; ok {
			a.Path = d.path
			a.FromRoot = d.fromRoot
			// A directory the plugin defines carries its own area, and a file in a
			// directory is in whatever area that directory is — so it settles the copy's
			// area, the same way it settles fromRoot.
			a.Area = d.area
		}
		out = append(out, a)
	}
	return out, nil
}

// sourcePathActions maps [SourcePaths]: sourcePath = outputTreePath. Each entry copies a
// file from the Windows source addressed by its exact path — relative to the source's
// install folder (i386) — to a literal output-tree path. It reaches files whose basename
// is not unique in the source (nested side-by-side assemblies, duplicate names) that
// [SourceDisksFiles]'s name lookup cannot resolve; the compressed .??_ form is handled
// like any source copy. A line missing either side is skipped.
func sourcePathActions(f *inf.File, build int) ([]action.Action, error) {
	var out []action.Action
	for _, l := range f.Section("SourcePaths", build) {
		src := normalizeTreePath(l.Key)
		dest := normalizeTreePath(l.Field(0))
		if src == "" || dest == "" {
			// These are deliberate, exact entries; a half-written one is a typo, and a
			// silent skip would surface far downstream (the copy just never happens).
			slog.Warn("[SourcePaths] entry needs both a source and a destination path; skipping",
				"source", src, "dest", dest)
			continue
		}
		out = append(out, action.Action{Kind: action.CopyFile, SrcPath: src, Dest: dest})
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
		// A line with only a type and subkey (no value-name field) creates the key
		// only; three or more fields carry a value, even when it is empty.
		a := action.Action{
			Kind:      action.AddReg,
			Hive:      hive,
			Subkey:    l.Field(1),
			ValueName: l.Field(2),
			RegType:   int(regType),
			HasValue:  len(l.Fields()) >= 3,
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
