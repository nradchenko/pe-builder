// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package layout reads a Windows installation source's own setup metadata —
// txtsetup.sif and layout.inf — into a resolution model the file-tree assembler
// consumes. It answers, for a target filename: which output directory it belongs
// in, and (via BootDrivers) which drivers a minimal NT boot loads.
//
// The two metadata files are ordinary setupapi INFs, so they are parsed with the
// inf package rather than a bespoke reader. txtsetup.sif is authoritative for the
// directory-ID table and the driver *.Load lists; layout.inf and txtsetup.sif both
// carry [SourceDisksFiles], and their per-file target directories agree — layout
// seeds from layout.inf and lets a non-empty txtsetup.sif directory win.
package layout

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/source"
)

// loadClasses are the txtsetup.sif driver *.Load section base names, in the order a
// minimal NT boot consults them. Each section maps a driver key to the driver's
// filename; BootDrivers unions their right-hand-side filenames.
//
// The set spans every driver class the setup loader loads, not just the storage
// controllers: the CD-ROM class and CD filesystem (ScsiClass, CdRomDrivers) are
// what let the booted kernel read a boot CD, so they belong here alongside the
// disk/bus/HAL classes.
var loadClasses = []string{
	"Hal",
	"BootBusExtenders",
	"BusExtenders",
	"SCSI",
	"DiskDrivers",
	"ScsiClass",
	"CdRomDrivers",
	"FloppyDrivers",
	"FileSystems",
	"InputDevicesSupport",
	"Keyboard",
	"MouseDrivers",
	"Display",
}

// defaultDir is the directory ID used for a source file whose [SourceDisksFiles]
// entry leaves the target-directory field empty. The core OS binaries that do so
// (e.g. ntoskrnl.exe) live in system32.
const defaultDir = "2"

// archSuffixes maps a source architecture to the setupapi section suffix the
// metadata uses for its architecture-specific [SourceDisksFiles] variant. Some
// files (notably hal.dll) are listed only under the arch-suffixed section, so it is
// folded in alongside the unsuffixed one.
var archSuffixes = map[string]string{
	"i386":  "x86",
	"amd64": "amd64",
	"ia64":  "ia64",
}

// FileEntry is one source file's placement, as declared by the source metadata.
type FileEntry struct {
	// Name is the canonical (decompressed) filename, lowercased — e.g. "ntoskrnl.exe".
	Name string
	// TargetDir is the [WinntDirectories] directory ID the file is placed in (see
	// Layout.Dirs). Empty in the metadata is normalized to defaultDir (system32).
	TargetDir string
}

// Layout is the parsed source metadata: the directory-ID table, the per-file
// placement map, and the boot-driver load classes.
type Layout struct {
	// Dirs maps a [WinntDirectories] directory ID to its path relative to the
	// system root, using forward slashes. The system root itself (ID "1") maps to "".
	Dirs map[string]string
	// Files maps a canonical filename to its placement.
	Files map[string]FileEntry
	// Load maps a load class (e.g. "FileSystems") to its driver filenames in
	// declaration order, deduplicated.
	Load map[string][]string
	// NLS is the source's national-language configuration (code pages, OEM console
	// font, language ID), read from txtsetup.sif's [nls] section.
	NLS NLS
}

// NLS is a source's national-language configuration from txtsetup.sif's [nls]
// section: the code-page tables and OEM console font its locale needs, and the
// language ID that identifies the locale.
type NLS struct {
	AnsiCodepageFile string // ANSI code-page table, e.g. c_1251.nls
	OemCodepageFile  string // OEM code-page table, e.g. c_866.nls
	OemHalFont       string // OEM console/HAL raster font, e.g. vga866.fon
	LangID           string // language ID from the Unicode case table, e.g. 0419
}

// KLID returns the source locale's keyboard-layout ID — the eight-hex-digit
// "0000"+LANGID form the registry uses (e.g. 00000419 for 0419) — or "" if the
// source declared no language ID.
func (n NLS) KLID() string {
	if n.LangID == "" {
		return ""
	}
	return "0000" + strings.ToUpper(n.LangID)
}

// Parse reads the source's txtsetup.sif and layout.inf and builds the model. The
// identity's build number selects any .BuildNo conditional section variants and its
// architecture selects the source folder (defaulting to i386).
func Parse(src source.Backing, id source.Identity) (*Layout, error) {
	// The metadata is part of the OS payload, so it lives in the source's system
	// area — the architecture's own folder, and the i386 folder when the identity
	// carries no architecture.
	arch := id.Roots().System

	txt, err := parseSourceINF(src, arch+"/txtsetup.sif")
	if err != nil {
		return nil, err
	}
	lay, err := parseSourceINF(src, arch+"/layout.inf")
	if err != nil {
		return nil, err
	}

	l := &Layout{
		Dirs:  map[string]string{},
		Files: map[string]FileEntry{},
		Load:  map[string][]string{},
	}
	suffix := archSuffixes[arch]
	l.readDirs(txt, id.Build)
	// layout.inf seeds placement; a non-empty txtsetup.sif directory then wins.
	l.readFiles(lay, id.Build, suffix)
	l.readFiles(txt, id.Build, suffix)
	l.readLoad(txt, id.Build)
	l.readNLS(txt, id.Build)
	return l, nil
}

// readNLS fills NLS from txtsetup.sif's [nls] section: the ANSI/OEM code-page table
// files (field 0 of each entry) and OEM console font, and the language ID carried as
// the second field of the Unicode case table entry.
func (l *Layout) readNLS(f *inf.File, build int) {
	for _, ln := range f.Section("nls", build) {
		switch {
		case strings.EqualFold(ln.Key, "AnsiCodepage"):
			l.NLS.AnsiCodepageFile = strings.TrimSpace(ln.Field(0))
		case strings.EqualFold(ln.Key, "OemCodepage"):
			l.NLS.OemCodepageFile = strings.TrimSpace(ln.Field(0))
		case strings.EqualFold(ln.Key, "OemHalFont"):
			l.NLS.OemHalFont = strings.TrimSpace(ln.Field(0))
		case strings.EqualFold(ln.Key, "UnicodeCasetable"):
			l.NLS.LangID = strings.TrimSpace(ln.Field(1))
		}
	}
}

// parseSourceINF reads and parses an INF from the source backing.
func parseSourceINF(src source.Backing, name string) (*inf.File, error) {
	data, err := fs.ReadFile(src, name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	f, err := inf.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	return f, nil
}

// readDirs fills Dirs from [WinntDirectories]: "id = path". The system-root value
// (a lone backslash) becomes "".
func (l *Layout) readDirs(f *inf.File, build int) {
	for _, ln := range f.Section("WinntDirectories", build) {
		id := strings.TrimSpace(ln.Key)
		if id == "" {
			continue
		}
		l.Dirs[id] = normDirPath(ln.Field(0))
	}
}

// readFiles upserts Files from [SourceDisksFiles] and its architecture-specific
// variant ([SourceDisksFiles.<archSuffix>], where files such as hal.dll live): the
// target-directory ID is field 7. A file may be listed more than once (across the
// metadata's several [SourceDisksFiles] blocks and between the two files); a
// non-empty directory is never overwritten by a later empty one, so auxiliary
// presence-check blocks that list a bare filename do not clobber the real placement.
func (l *Layout) readFiles(f *inf.File, build int, archSuffix string) {
	lines := f.Section("SourceDisksFiles", build)
	if archSuffix != "" {
		// Pass build so any .BuildNo variant of the arch section folds in too, exactly
		// as it does for the unsuffixed section above.
		lines = append(lines, f.Section("SourceDisksFiles."+archSuffix, build)...)
	}
	for _, ln := range lines {
		name := strings.ToLower(strings.TrimSpace(ln.Key))
		if name == "" {
			continue
		}
		dir := strings.TrimSpace(ln.Field(7))
		if _, ok := l.Files[name]; ok && dir == "" {
			// Keep the placement we already have rather than blanking it.
			continue
		}
		l.Files[name] = FileEntry{Name: name, TargetDir: dir}
	}
}

// readLoad fills Load from the *.Load sections, collecting each line's filename
// (field 0 of the value) per class in declaration order, deduplicated.
func (l *Layout) readLoad(f *inf.File, build int) {
	for _, class := range loadClasses {
		var files []string
		seen := map[string]bool{}
		for _, ln := range f.Section(class+".Load", build) {
			name := strings.ToLower(strings.TrimSpace(ln.Field(0)))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			files = append(files, name)
		}
		if files != nil {
			l.Load[class] = files
		}
	}
}

// Resolve returns the placement of the named file (matched case-insensitively), and
// whether it is known. The returned TargetDir is normalized: an empty metadata
// directory becomes system32.
func (l *Layout) Resolve(name string) (FileEntry, bool) {
	e, ok := l.Files[strings.ToLower(name)]
	if !ok {
		return FileEntry{}, false
	}
	if e.TargetDir == "" {
		e.TargetDir = defaultDir
	}
	return e, true
}

// DirPath returns the path (relative to the system root) for a directory ID, and
// whether the ID is known.
func (l *Layout) DirPath(id string) (string, bool) {
	p, ok := l.Dirs[id]
	return p, ok
}

// BootDrivers returns the union of every load class's driver filenames, in class
// order then declaration order, deduplicated across classes.
func (l *Layout) BootDrivers() []string {
	var out []string
	seen := map[string]bool{}
	for _, class := range loadClasses {
		for _, name := range l.Load[class] {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// ReadSourceFile resolves a source file under the system root by name, trying its
// loose (uncompressed) name first and its single-file *.??_ archive name second.
// It reports the raw bytes, whether they are still the compressed archive form
// (which the caller decompresses, unless it deliberately wants the packed bytes),
// and whether the file was found. It is the shared loose→packed resolution both
// the tree assembler and the hive stage read through, so the two never diverge on
// where a source file lives or how its compressed name is formed. Cabinet-only
// files (many drivers) are a further fallback the tree assembler layers on top.
func ReadSourceFile(b source.Backing, name string) (data []byte, packed, found bool) {
	loose := path.Join(source.SystemRoot, name)
	if d, err := fs.ReadFile(b, loose); err == nil {
		return d, false, true
	}
	archive := path.Join(source.SystemRoot, CompressedName(name))
	if d, err := fs.ReadFile(b, archive); err == nil {
		return d, true, true
	}
	return nil, false, false
}

// CompressedName returns the source media's compressed name for a canonical
// filename: the last character is replaced with an underscore (ntdll.dll →
// ntdll.dl_), the setupapi convention for a single-file *.??_ cabinet. It is only a
// candidate — a file may also ship uncompressed under its canonical name, so callers
// probe the source for the loose name first.
func CompressedName(name string) string {
	if name == "" {
		return name
	}
	return name[:len(name)-1] + "_"
}

// normDirPath converts a [WinntDirectories] path to a forward-slash path relative to
// the system root. The root marker (a lone backslash) becomes "".
func normDirPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.Trim(p, "/")
	return p
}
