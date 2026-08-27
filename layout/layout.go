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
//
// One further question the media answer for themselves is read here too, because it
// is the same kind of answer: whether the source is a workstation or a server product
// (see readProductType), which the media state in hivesys.inf and txtsetup.sif.
package layout

import (
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	"github.com/nradchenko/pe-builder/compress"
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
	// DestName is the name setup installs the file under, when the metadata declares one
	// different from the name it is stored under; empty when the two are the same, which
	// is the common case. Lowercased.
	//
	// It is not a curiosity. A localized medium ships the service-pack resource satellites
	// under language-coded names — sprt0419.dll on Russian media, 0419 being the LCID —
	// and declares that each is installed as the canonical name every consumer loads by
	// (xpsp2res.dll). A build that places them under the stored name leaves that consumer
	// nothing to find, and the medium looks as though it never carried the file.
	DestName string
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
	// ServerProduct reports whether the source is a server product, from the product type
	// the media's own hivesys.inf declares or, for a source that declares none, from
	// txtsetup.sif's [SetupData] ProductType (see readProductType). It is the media's own
	// answer, and the only thing that separates XP Professional x64 Edition from Server
	// 2003 x64.
	ServerProduct bool
}

// NLS is a source's national-language configuration from txtsetup.sif's [nls]
// section: the code-page tables and OEM console font its locale needs, and the
// language ID that identifies the locale.
type NLS struct {
	AnsiCodepageFile string // ANSI code-page table, e.g. c_1251.nls
	AnsiCodepage     string // ANSI code-page number the table serves, e.g. 1251
	OemCodepageFile  string // OEM code-page table, e.g. c_866.nls
	OemCodepage      string // OEM code-page number the table serves, e.g. 866
	// OemAlternates holds the further OEM pairs the [nls] line names beyond the
	// locale's own, which is the one above: Russian media state 866 then 437, Arabic
	// 720 then 850. They are not what the locale runs in, but the source installs them
	// alongside it, so a build that means to offer them has to ship and name them too.
	OemAlternates []CodePage
	OemHalFont    string // OEM console/HAL raster font, e.g. vga866.fon
	LangID        string // language ID from the Unicode case table, e.g. 0419
}

// CodePage is one code-page table a source names: the file holding it and the number
// it serves.
type CodePage struct {
	File   string // table file, e.g. c_437.nls
	Number string // the code page it serves, e.g. 437
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
	l.readProductType(src, id, txt)
	return l, nil
}

// ntProductTypes classifies the product-type strings ProductOptions can carry, reporting
// whether each names a server. NT defines exactly these three: a workstation, a stand-alone
// server, and a domain controller.
var ntProductTypes = map[string]bool{
	"winnt":    false,
	"servernt": true,
	"lanmannt": true,
}

// setupDataProductTypes classifies the [SetupData] ProductType values measured on real
// media. The field enumerates the SKU rather than answering workstation-or-server, and no
// mapping of it is published, so it is read against the values actually seen rather than by
// a rule: an unlisted value means a SKU nobody here has measured. Reading every non-zero
// value as a server — as this once did — brands a workstation SKU ServerNT, and that
// misbrands more than the registry: the kernel then reports VER_NT_SERVER, so the shell's
// splash asks msgina.dll for the server banner, which on XP media is stale pre-release
// artwork.
var setupDataProductTypes = map[int64]bool{
	0: false, // XP Professional, x86 and x64 alike; the XP SP3 OPK
	4: false, // XP Home Edition and XP Starter Edition, which state the same value
	1: true,  // Server 2003 (Standard), x86 and amd64 alike
	6: true,  // Small Business Server 2003
}

// readProductType fills ServerProduct from the media's own answers. Two exist, and they are
// not equally good.
//
// hivesys.inf — the GUI-mode system config the hive stage folds onto SYSTEM — states the
// product-type string itself, so it needs no decoding and holds for any SKU. Every XP-branch
// medium measured declares "WinNt" there, XP Professional x64 Edition included, which is the
// case this derivation exists for at all. It is preferred for that reason: it answers for XP
// SKUs whose [SetupData] value has never been measured.
//
// Server 2003 media declare no ProductOptions at all (measured on Standard, x86 and amd64),
// so a silent source falls back to txtsetup.sif's [SetupData] ProductType and the SKU values
// measured for it. A value that is not among them is reported rather than guessed at, and
// settles on the workstation answer — which is what every build produced before this was
// read at all.
//
// The declared string decides only which product this is; the value written to the built
// hive stays base's own, because the string the media declares is not the one an installed
// system carries: hivesys.inf states "WinNt" where a real XP SYSTEM hive holds "WinNT".
func (l *Layout) readProductType(src source.Backing, id source.Identity, txt *inf.File) {
	if s, ok := declaredProductType(src, id); ok {
		if server, known := ntProductTypes[strings.ToLower(s)]; known {
			l.ServerProduct = server
			return
		}
		slog.Warn("source declares an unknown NT product type; falling back to its SKU value",
			"producttype", s)
	}

	v, ok := setupDataProductType(txt, id.Build)
	if !ok {
		return // nothing stated anywhere: the workstation answer, as before
	}
	server, known := setupDataProductTypes[v]
	if !known {
		slog.Warn("source states an unrecognized SKU and declares no product type; building as a workstation",
			"setupdata_producttype", v)
		return
	}
	l.ServerProduct = server
}

// setupDataProductType reports txtsetup.sif's [SetupData] ProductType, and whether the
// source stated a parsable one.
func setupDataProductType(f *inf.File, build int) (int64, bool) {
	var v int64
	var found bool
	for _, ln := range f.Section("SetupData", build) {
		if strings.EqualFold(ln.Key, "ProductType") {
			if n, ok := inf.ParseInt(strings.TrimSpace(ln.Field(0))); ok {
				v, found = n, true
			}
		}
	}
	return v, found
}

// declaredProductType reports the product-type string the source's hivesys.inf writes to
// ControlSet001\Control\ProductOptions, and whether it declares one at all. A source that
// cannot be read for it is treated as declaring nothing: this is a preferred answer, not a
// required one, and the caller has a second.
func declaredProductType(src source.Backing, id source.Identity) (string, bool) {
	data, packed, found := ReadSourceFile(src, id.Roots(), source.AreaSystem, "hivesys.inf")
	if !found {
		return "", false
	}
	if packed {
		d, err := compress.DecompressBytes(data)
		if err != nil {
			return "", false
		}
		data = d
	}
	f, err := inf.Parse(data)
	if err != nil {
		return "", false
	}
	// The lines are setupapi AddReg directives: root, subkey, value name, flags, data.
	for _, ln := range f.Section("AddReg", 0) {
		fields := ln.Fields()
		if len(fields) < 5 || !strings.EqualFold(fields[0], "HKLM") {
			continue
		}
		if !strings.EqualFold(fields[2], "ProductType") || !isProductOptions(fields[1]) {
			continue
		}
		if s := strings.TrimSpace(fields[4]); s != "" {
			return s, true
		}
	}
	return "", false
}

// isProductOptions reports whether a hivesys.inf subkey path names the SYSTEM hive's
// Control\ProductOptions, under either the CurrentControlSet the INF writes or the real
// control set an offline hive carries.
func isProductOptions(p string) bool {
	p = strings.Trim(p, `\`)
	parts := strings.Split(p, `\`)
	if len(parts) != 4 || !strings.EqualFold(parts[0], "SYSTEM") {
		return false
	}
	return (strings.EqualFold(parts[1], "CurrentControlSet") || strings.EqualFold(parts[1], "ControlSet001")) &&
		strings.EqualFold(parts[2], "Control") && strings.EqualFold(parts[3], "ProductOptions")
}

// readNLS fills NLS from txtsetup.sif's [nls] section: the ANSI/OEM code-page tables,
// each stated as a file and the number it serves (fields 0 and 1), the OEM console
// font, and the language ID carried as the second field of the Unicode case table
// entry.
func (l *Layout) readNLS(f *inf.File, build int) {
	for _, ln := range f.Section("nls", build) {
		switch {
		case strings.EqualFold(ln.Key, "AnsiCodepage"):
			l.NLS.AnsiCodepageFile = strings.TrimSpace(ln.Field(0))
			l.NLS.AnsiCodepage = strings.TrimSpace(ln.Field(1))
		case strings.EqualFold(ln.Key, "OemCodepage"):
			l.NLS.OemCodepageFile = strings.TrimSpace(ln.Field(0))
			l.NLS.OemCodepage = strings.TrimSpace(ln.Field(1))
			// Any further file/number pairs on the same line are alternates the source
			// installs beside the locale's own (Arabic states 720 then 850); the first
			// pair above is the one the locale runs in.
			for i := 2; ; i += 2 {
				file := strings.TrimSpace(ln.Field(i))
				number := strings.TrimSpace(ln.Field(i + 1))
				if file == "" || number == "" {
					break
				}
				l.NLS.OemAlternates = append(l.NLS.OemAlternates, CodePage{File: file, Number: number})
			}
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
		// Field 10 is the name setup installs the file under, when that differs from the
		// name it is stored under. Usually empty; see FileEntry.DestName for why it is
		// not optional to honour.
		dest := strings.ToLower(strings.TrimSpace(ln.Field(10)))
		if dest == name {
			dest = "" // declared, but the same name — nothing to carry
		}
		prev, seen := l.Files[name]
		if seen && dir == "" {
			// Keep the placement we already have rather than blanking it — but a later
			// block may still be the one that names the destination.
			if dest != "" && prev.DestName == "" {
				prev.DestName = dest
				l.Files[name] = prev
			}
			continue
		}
		// Neither does a later block blank a destination already recorded: the two fields
		// are independent, and a repeat that carries a directory but no name says nothing
		// about the name. Dropping it would put the file back under its stored name.
		if dest == "" && seen {
			dest = prev.DestName
		}
		l.Files[name] = FileEntry{Name: name, TargetDir: dir, DestName: dest}
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

// StoredAs returns the name a file is stored under on the medium, given the name it is
// installed as, and whether the metadata declares such a mapping. For the overwhelming
// majority of files the two are the same and this reports false — it answers only for the
// files a source keeps under one name and installs under another.
//
// It is the lookup a caller needs when it knows a file by the name things load it by. Ask
// a localized medium for xpsp2res.dll and nothing matches; the file is there as
// sprt0419.dll, and only the metadata connects the two.
//
// It answers only when the mapping is unambiguous. A name can be claimed by many source
// files at once — 23 of Server 2003's declare ws03res.dll, one per MUI language, each
// placed in its own directory — and nothing in the name alone says which was meant.
// Guessing there would pick by map order, which is to say differently on each run; a
// caller whose direct lookup already failed is better served by the miss it was going to
// get anyway.
func (l *Layout) StoredAs(installed string) (string, bool) {
	want := strings.ToLower(installed)
	// No name is not a name. A [SourcePaths] copy addresses the source by an exact path and
	// carries no file name at all, and asking for "" would match every entry that declares
	// no rename — which is nearly all of them. The ambiguity rule below happens to catch
	// that, but only by accident, and one such entry on its own would slip through.
	if want == "" {
		return "", false
	}
	stored := ""
	for _, e := range l.Files {
		if e.DestName != want {
			continue
		}
		if stored != "" {
			return "", false // claimed more than once; see above
		}
		stored = e.Name
	}
	return stored, stored != ""
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

// ReadSourceFile resolves a source file by name, trying its loose (uncompressed)
// name first and its single-file *.??_ archive name second. It reports the raw
// bytes, whether they are still the compressed archive form (which the caller
// decompresses, unless it deliberately wants the packed bytes), and whether the file
// was found. It is the shared loose→packed resolution both the tree assembler and
// the hive stage read through, so the two never diverge on where a source file lives
// or how its compressed name is formed. Cabinet-only files (many drivers) are a
// further fallback the tree assembler layers on top.
//
// area says which of the source's folders to look in, and stays the caller's choice —
// it knows what the file is. Naming the area rather than passing a folder means a
// caller cannot hand over a string that is not one of the source's roots.
func ReadSourceFile(b source.Backing, roots source.Roots, area source.Area, name string) (data []byte, packed, found bool) {
	root := roots.For(area)
	loose := path.Join(root, name)
	if d, err := fs.ReadFile(b, loose); err == nil {
		return d, false, true
	}
	archive := path.Join(root, CompressedName(name))
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
