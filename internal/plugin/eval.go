// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/source"
)

// SourceDisksFiles attribute bits. 1–8 are the original format's
// (see docs/bartpe/03-plugin-format.md); 16 is a pebuild extension.
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
	// attrAdaptNetworkINF rewrites a staged network-component INF for a PE (see
	// inf.AdaptNetworkComponent, which documents which files it is meaningful on — it is
	// not a general "adapt any INF" flag). A second pebuild extension above the original
	// format's 1–8, for the same reason as attrBootArea: a plugin written for the original
	// tool never sets it and is read unchanged, while one that does is not portable back —
	// the original tool has no component install to adapt an INF for.
	attrAdaptNetworkINF = 32
	// attrToBitmap re-encodes a staged image as a 256-colour BMP (see tree.ToBitmap). The
	// classic wallpaper path paints bitmaps only, and 64-bit Windows has no Active Desktop
	// to paint anything else, so a source's JPEG has to become a BMP for a wallpaper to
	// show on every architecture. Windows converts it the same way when a user picks a
	// non-BMP image; this does it at build time instead, which is why nothing in the image
	// needs a decoder. A third pebuild extension above the original format's 1–8, for the
	// same reason as the two above.
	attrToBitmap = 64
)

// Target is what an INF is compiled for: the source's build number, which selects
// .BuildNo section variants, and its architecture, which selects .<arch> ones. It is
// one value rather than two parameters so a further axis does not have to be threaded
// through every section reader again.
type Target struct {
	// Build is the source's Windows build number; 0 selects no .BuildNo variant.
	Build int
	// Arch is the source's architecture ("i386", "amd64"). An unset one resolves to
	// the x86 layout, exactly as it does for a source's folders — see
	// source.ResolveArch, which both go through.
	Arch string
}

// TargetFor derives the compilation target from a probed source identity. The base
// layer and the plugin layer both build their target through it, so one source can
// never resolve a different set of sections in one than in the other.
func TargetFor(id source.Identity) Target {
	return Target{Build: id.Build, Arch: id.Arch}
}

// validate rejects an architecture that no source can have. An unrecognized one would
// otherwise match no section and quietly compile the architecture-neutral content
// alone — which, for the base layer, means dropping the very options that make the
// output a PE. Everything the engine builds comes from a probe and is always valid;
// this catches a hand-built identity or a command-line flag.
func (t Target) validate() error {
	if a := source.ResolveArch(t.Arch); !source.KnownArch(a) {
		return fmt.Errorf("unknown architecture %q", t.Arch)
	}
	return nil
}

// sections returns a section's lines followed by those of its architecture variant,
// so an INF adds arch-specific entries without repeating the shared ones. It mirrors
// how the source's own metadata reads its arch sections, and how inf.File.Section
// folds in a .BuildNo variant — the same compose rule at each level.
//
// It applies to the sections that compile to actions. The metadata sections a plugin
// declares ([Build], [Closure], [Options], [Update]) are read per build only.
func (t Target) sections(f *inf.File, base string) []*inf.Line {
	lines := f.Section(base, t.Build)
	return append(lines, f.Section(base+"."+source.ResolveArch(t.Arch), t.Build)...)
}

// WinntDirectories attribute bits. 1 and 2 are the original format's; 16 is the
// same pebuild extension as attrBootArea, kept at the same value so one number means
// "boot chain" in both sections.
const (
	dirAlwaysCreate = 1 // create even when empty
	dirFromRoot     = 2 // create at the output root, not under the area's folder
	dirBootArea     = 16
)

// Actions compiles a plugin's copy/registry/text sections into the action IR for
// the given target. Sections are visited in a fixed order (directories, file
// copies, registry edits, then text patches); within a section, file order is
// preserved. .BuildNo variants are folded in by inf.File.Section, and .<arch> ones
// by Target.sections.
func Actions(f *inf.File, t Target) ([]action.Action, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	var out []action.Action
	add := func(as []action.Action, err error) error {
		if err != nil {
			return err
		}
		out = append(out, as...)
		return nil
	}

	if err := add(dirsActions(f, t)); err != nil {
		return nil, err
	}
	// A copy may target a directory the plugin defines itself in [WinntDirectories]
	// (a letter or number ID), not one the Windows source's txtsetup.sif knows, so it
	// is resolved against the plugin's own table here rather than the source layout.
	if err := add(copyActions(f, t, winntDirs(f, t))); err != nil {
		return nil, err
	}
	if err := add(sourcePathActions(f, t)); err != nil {
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
		if err := add(regActions(f, t, r.section, r.hive)); err != nil {
			return nil, err
		}
	}
	if err := add(setValueActions(f, t)); err != nil {
		return nil, err
	}
	if err := add(lineActions(f, t, "AddLine", action.AddLine)); err != nil {
		return nil, err
	}
	if err := add(lineActions(f, t, "DelLine", action.DelLine)); err != nil {
		return nil, err
	}
	if err := add(appendActions(f, t)); err != nil {
		return nil, err
	}
	return out, nil
}

// dirsActions maps [WinntDirectories]: directoryID=dirname[, attribute].
func dirsActions(f *inf.File, t Target) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, "WinntDirectories") {
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
func winntDirs(f *inf.File, t Target) map[string]dirTarget {
	dirs := make(map[string]dirTarget)
	for _, l := range t.sections(f, "WinntDirectories") {
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
func copyActions(f *inf.File, t Target, dirs map[string]dirTarget) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, "SourceDisksFiles") {
		attr, _ := l.IntField(2)
		a := action.Action{
			Kind:            action.CopyFile,
			Src:             l.Key,
			DirID:           l.Field(0),
			Rename:          l.Field(1),
			ShipWithPlugin:  attr&attrShipWithPlugin != 0,
			NoDecompress:    attr&attrNoDecompress != 0,
			Optional:        attr&attrOptional != 0,
			Uppercase:       attr&attrUppercase != 0,
			AdaptNetworkINF: attr&attrAdaptNetworkINF != 0,
			ToBitmap:        attr&attrToBitmap != 0,
			Area:            bootAreaAttr(attr, attrBootArea),
		}
		// Attribute 2 keeps the compressed archive; attribute 32 rewrites the file's text.
		// Together they would run a text transform over MSCF bytes and stage the result as a
		// cabinet, which is not a thing anyone can want — rejected here, where the line and
		// its plugin can be named, rather than silently producing a corrupt archive.
		if a.NoDecompress && a.AdaptNetworkINF {
			return nil, fmt.Errorf("[SourceDisksFiles]: %q cannot be both kept compressed "+
				"(attribute 2) and adapted for a PE (attribute 32)", a.Src)
		}
		// The same argument for the same reason: re-encoding an image means decoding it
		// first, and there is nothing to decode in an MSCF archive.
		if a.NoDecompress && a.ToBitmap {
			return nil, fmt.Errorf("[SourceDisksFiles]: %q cannot be both kept compressed "+
				"(attribute 2) and re-encoded as a bitmap (attribute 64)", a.Src)
		}
		// Both transforms rewrite the same bytes, and neither is meaningful on the other's
		// input — an INF is not an image and an image is not text.
		if a.AdaptNetworkINF && a.ToBitmap {
			return nil, fmt.Errorf("[SourceDisksFiles]: %q cannot be both adapted for a PE "+
				"(attribute 32) and re-encoded as a bitmap (attribute 64)", a.Src)
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
func sourcePathActions(f *inf.File, t Target) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, "SourcePaths") {
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
func regActions(f *inf.File, t Target, prefix string, hive action.RegHive) ([]action.Action, error) {
	var out []action.Action

	for _, l := range t.sections(f, prefix+".AddReg") {
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

	for _, l := range t.sections(f, prefix+".DelReg") {
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
func setValueActions(f *inf.File, t Target) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, "SetValue") {
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
func lineActions(f *inf.File, t Target, section string, kind action.Kind) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, section) {
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
func appendActions(f *inf.File, t Target) ([]action.Action, error) {
	var out []action.Action
	for _, l := range t.sections(f, "Append") {
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
