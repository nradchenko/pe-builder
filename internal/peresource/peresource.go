// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package peresource reads the resource directory of a Windows PE image.
//
// A PE keeps its resources in a three-level tree — type, then name or id, then
// language — and what sits at each level is load-bearing rather than incidental. The
// Windows loader takes a process's manifest from the resource *numbered* 1 and, having
// found one, ignores an external manifest file beside the binary; a manifest at any
// other id is read only by something that asks for it. So "does this binary carry a
// manifest" cannot be answered without saying which id it is at.
//
// Reading that answer out of a binary is otherwise a job for a hex editor: this package
// exists so it is not hand-parsed at each call site, an approach that has already
// produced a confidently wrong answer here (an empty result from a binary that carried
// two manifests).
package peresource

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// Resource types, from winuser.h. Only the ones this project reads are named.
const (
	// TypeVersion is RT_VERSION, the VS_VERSIONINFO block.
	TypeVersion = 16
	// TypeManifest is RT_MANIFEST, a side-by-side assembly manifest.
	TypeManifest = 24
)

// The manifest ids the loader itself gives meaning to, from winuser.h. An id outside
// this set is a manifest only to whoever goes looking for it by number.
const (
	// CreateProcessManifest is the manifest the loader applies to a process. Finding
	// one here is what makes the loader ignore a <binary>.manifest file on disk —
	// even when what it finds is a stub with nothing in it.
	CreateProcessManifest = 1
	// IsolationAwareManifest is the manifest a DLL is activated with.
	IsolationAwareManifest = 2
	// IsolationAwareNoStaticImportManifest is the same, for a DLL loaded without its
	// static imports.
	IsolationAwareNoStaticImportManifest = 3
)

// Entry is one resource leaf: what identifies it, the language it declares, and its
// bytes.
type Entry struct {
	// ID is the resource's number, when it is numbered.
	ID int
	// Name is the resource's name, when it is named rather than numbered. A resource
	// has one or the other, never both.
	Name string
	// Lang is the language id of this leaf. 0 is language-neutral, which is what a
	// manifest usually is.
	Lang int
	// Data is the resource's bytes.
	Data []byte
}

// Named reports whether the resource is identified by name rather than by number.
func (e Entry) Named() bool { return e.Name != "" }

// String identifies the resource the way an error or a listing should: by whichever
// of name or id it actually has.
func (e Entry) String() string {
	if e.Named() {
		return fmt.Sprintf("%q", e.Name)
	}
	return fmt.Sprintf("id %d", e.ID)
}

// subdirBit flags a resource directory entry as pointing at a subdirectory rather than
// at a leaf. On the Name field it flags a named entry rather than a numbered one.
const subdirBit = 0x80000000

// dirHeaderSize is IMAGE_RESOURCE_DIRECTORY: two dwords, two words of version, then
// the named and id entry counts.
const dirHeaderSize = 16

// dirEntrySize is IMAGE_RESOURCE_DIRECTORY_ENTRY: Name and OffsetToData.
const dirEntrySize = 8

// dataEntrySize is IMAGE_RESOURCE_DATA_ENTRY: an RVA, a size, a code page, a reserved
// dword.
const dataEntrySize = 16

// maxLeaves caps how many resources of one type will be returned.
//
// Each level of the directory is bounded against the data it sits in, but not against
// the others: a corrupt directory whose every level-2 entry points at the same large
// level-3 directory yields the product of the two, which for a megabyte of resources is
// ten billion leaves and an allocation no machine will satisfy. These binaries come off
// user-supplied media, so that has to be a refusal rather than a hang. No real binary
// carries anything near this many resources of one type.
const maxLeaves = 65536

// ReadFirst returns the first resource of the given type, stopping there: it does not
// read, or validate, anything after it. ok is false when the binary carries no resource
// of the type.
//
// This is for a caller that wants one known resource — a version block, say — where a
// second malformed one elsewhere in the binary is not its problem. Read is the stricter
// reading, and reports such a binary as broken.
func ReadFirst(image []byte, typ int) (Entry, bool, error) {
	entries, err := read(image, typ, 1)
	if err != nil || len(entries) == 0 {
		return Entry{}, false, err
	}
	return entries[0], true, nil
}

// Read returns every resource of the given type, in the order the directory lists
// them: named entries first, then numbered, each in the order the linker wrote.
//
// A binary carrying no resources at all, or none of this type, is not an error — it is
// an empty result. Only a file that cannot be read as a PE, or a resource directory
// that contradicts itself, is.
func Read(image []byte, typ int) ([]Entry, error) {
	return read(image, typ, maxLeaves)
}

// read is Read, stopping once limit leaves have been collected.
func read(image []byte, typ, limit int) ([]Entry, error) {
	f, err := pe.NewFile(bytes.NewReader(image))
	if err != nil {
		return nil, fmt.Errorf("parse PE: %w", err)
	}
	defer f.Close()

	rsrc, base, ok, err := resourceSection(f)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	// Level 1 is the type. Absent means the binary has no resource of this type,
	// which is an answer rather than a failure.
	typeEntry, ok, err := entryByID(rsrc, 0, typ)
	if err != nil {
		return nil, fmt.Errorf("resource types: %w", err)
	}
	if !ok {
		return nil, nil
	}
	if typeEntry&subdirBit == 0 {
		return nil, fmt.Errorf("resource type %d is a leaf, not a directory", typ)
	}

	// Level 2 identifies each resource, by name or by number; level 3 is its
	// languages. The walk is exactly three levels deep by construction, so a
	// directory that pointed back at itself cannot send it round in circles.
	var out []Entry
	sections := make(map[*pe.Section][]byte)
	named, err := entries(rsrc, int(typeEntry&^subdirBit))
	if err != nil {
		return nil, fmt.Errorf("resource type %d: %w", typ, err)
	}
	for _, id := range named {
		if len(out) >= limit {
			break
		}
		e := Entry{ID: int(id.id)}
		if id.named {
			if e.Name, err = dirString(rsrc, int(id.id)); err != nil {
				return nil, fmt.Errorf("resource type %d: %w", typ, err)
			}
			e.ID = 0
		}
		if id.target&subdirBit == 0 {
			return nil, fmt.Errorf("resource %s is a leaf where a language directory was expected", e)
		}
		langs, err := entries(rsrc, int(id.target&^subdirBit))
		if err != nil {
			return nil, fmt.Errorf("resource %s: %w", e, err)
		}
		for _, lang := range langs {
			if len(out) >= limit {
				break
			}
			if lang.target&subdirBit != 0 {
				return nil, fmt.Errorf("resource %s language %d is a directory where the data was expected",
					e, lang.id)
			}
			data, err := leafData(f, sections, rsrc, base, int(lang.target))
			if err != nil {
				return nil, fmt.Errorf("resource %s language %d: %w", e, lang.id, err)
			}
			leaf := e
			leaf.Lang = int(lang.id)
			leaf.Data = data
			out = append(out, leaf)
		}
	}
	return out, nil
}

// resourceSection returns the bytes of the section holding the resource directory and
// the RVA it starts at. ok is false when the image declares no resources.
//
// The section is found through the optional header's resource data directory rather
// than by the name ".rsrc", because the name is a convention and the directory is the
// format's own answer.
func resourceSection(f *pe.File) (data []byte, base uint32, ok bool, err error) {
	// DataDirectory is a fixed 16-entry array whatever the header says, so the count
	// that decides whether the resource entry means anything is NumberOfRvaAndSizes.
	// An image declaring fewer than three directories has no resource one.
	var dir pe.DataDirectory
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		if h.NumberOfRvaAndSizes <= pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			return nil, 0, false, nil
		}
		dir = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
	case *pe.OptionalHeader64:
		if h.NumberOfRvaAndSizes <= pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			return nil, 0, false, nil
		}
		dir = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
	default:
		// An object file has no optional header, so it declares no resources.
		return nil, 0, false, nil
	}
	if dir.VirtualAddress == 0 || dir.Size == 0 {
		return nil, 0, false, nil
	}

	sec := sectionAt(f, dir.VirtualAddress)
	if sec == nil {
		return nil, 0, false, fmt.Errorf("resource directory at %#x is in no section", dir.VirtualAddress)
	}
	b, err := sectionBytes(sec)
	if err != nil {
		return nil, 0, false, err
	}
	// The directory's offsets are all relative to its own start, which is where the
	// walk has to begin — normally the start of the section, but the format does not
	// require that.
	off := dir.VirtualAddress - sec.VirtualAddress
	if int(off) > len(b) {
		return nil, 0, false, fmt.Errorf("resource directory at %#x is past the end of section %s",
			dir.VirtualAddress, sec.Name)
	}
	return b[off:], dir.VirtualAddress, true, nil
}

// sectionAt returns the section containing an RVA, or nil.
func sectionAt(f *pe.File, rva uint32) *pe.Section {
	for _, s := range f.Sections {
		// A section's mapped size is its virtual size, except that a section whose
		// virtual size is zero is mapped at its raw size — which is how some older
		// linkers wrote them.
		size := s.VirtualSize
		if size == 0 {
			size = s.Size
		}
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+size {
			return s
		}
	}
	return nil
}

// sectionBytes returns a section's data trimmed to the size it occupies in memory.
// Section.Data returns the file-aligned bytes, which are padded, and resource offsets
// resolved against the padding would read whatever the linker left there.
func sectionBytes(s *pe.Section) ([]byte, error) {
	b, err := s.Data()
	if err != nil {
		return nil, fmt.Errorf("read section %s: %w", s.Name, err)
	}
	if n := int(s.VirtualSize); n > 0 && n < len(b) {
		b = b[:n]
	}
	return b, nil
}

// dirEntry is one IMAGE_RESOURCE_DIRECTORY_ENTRY: how the resource is identified, and
// what it points at.
type dirEntry struct {
	// id is the entry's number, or — when named is true — the offset of its name.
	id uint32
	// named reports whether id is a name offset rather than a number.
	named bool
	// target is the OffsetToData field, whose high bit marks a subdirectory.
	target uint32
}

// fits reports whether n bytes starting at off lie within a buffer of length size.
//
// The comparisons are made in uint64 rather than int because every offset here comes
// from a 31-bit field in the file: on a 32-bit build off+n wraps negative, and a check
// written that way passes exactly the hostile offsets it exists to reject.
func fits(off, n, size int) bool {
	if off < 0 || n < 0 || size < 0 {
		return false
	}
	return uint64(off)+uint64(n) <= uint64(size)
}

// entries reads every entry of the directory at off.
func entries(rsrc []byte, off int) ([]dirEntry, error) {
	if !fits(off, dirHeaderSize, len(rsrc)) {
		return nil, fmt.Errorf("directory at %#x is out of range", off)
	}
	named := int(binary.LittleEndian.Uint16(rsrc[off+12:]))
	ids := int(binary.LittleEndian.Uint16(rsrc[off+14:]))
	total := named + ids
	first := off + dirHeaderSize
	if !fits(first, total*dirEntrySize, len(rsrc)) {
		return nil, fmt.Errorf("directory at %#x declares %d entries, which run past the resource data",
			off, total)
	}
	out := make([]dirEntry, 0, total)
	for i := 0; i < total; i++ {
		e := first + i*dirEntrySize
		name := binary.LittleEndian.Uint32(rsrc[e:])
		out = append(out, dirEntry{
			id:     name &^ subdirBit,
			named:  name&subdirBit != 0,
			target: binary.LittleEndian.Uint32(rsrc[e+4:]),
		})
	}
	return out, nil
}

// entryByID finds the numbered entry with the given id in the directory at off. ok is
// false when the directory has no such entry, which is not an error.
func entryByID(rsrc []byte, off, id int) (target uint32, ok bool, err error) {
	es, err := entries(rsrc, off)
	if err != nil {
		return 0, false, err
	}
	for _, e := range es {
		if !e.named && int(e.id) == id {
			return e.target, true, nil
		}
	}
	return 0, false, nil
}

// dirString reads an IMAGE_RESOURCE_DIR_STRING_U: a character count, then that many
// UTF-16 code units. The count is in characters, not bytes.
func dirString(rsrc []byte, off int) (string, error) {
	if !fits(off, 2, len(rsrc)) {
		return "", fmt.Errorf("resource name at %#x is out of range", off)
	}
	n := int(binary.LittleEndian.Uint16(rsrc[off:]))
	start := off + 2
	if !fits(start, n*2, len(rsrc)) {
		return "", fmt.Errorf("resource name at %#x runs past the resource data", off)
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(rsrc[start+i*2:])
	}
	return string(utf16.Decode(u)), nil
}

// leafData reads the IMAGE_RESOURCE_DATA_ENTRY at off and returns the bytes it points
// at. Its OffsetToData is an RVA into the image rather than an offset within the
// resource directory, so it is resolved against the section that contains it — which
// is normally the resource section, but nothing in the format says it must be.
// sections caches each section's bytes, so a binary whose resource data sits outside
// the resource section does not have that section re-read and re-allocated once per
// leaf.
func leafData(f *pe.File, sections map[*pe.Section][]byte, rsrc []byte, base uint32, off int) ([]byte, error) {
	if !fits(off, dataEntrySize, len(rsrc)) {
		return nil, fmt.Errorf("data entry at %#x is out of range", off)
	}
	rva := binary.LittleEndian.Uint32(rsrc[off:])
	size := binary.LittleEndian.Uint32(rsrc[off+4:])

	// The common case: the data sits in the same section, which is already read. The
	// arithmetic is in uint64 because size is a full 32-bit field and would be
	// negative as an int on a 32-bit build.
	if rva >= base && uint64(rva-base)+uint64(size) <= uint64(len(rsrc)) {
		start := int(rva - base)
		return rsrc[start : start+int(size)], nil
	}

	sec := sectionAt(f, rva)
	if sec == nil {
		return nil, fmt.Errorf("data at %#x is in no section", rva)
	}
	b, ok := sections[sec]
	if !ok {
		var err error
		if b, err = sectionBytes(sec); err != nil {
			return nil, err
		}
		sections[sec] = b
	}
	start := uint64(rva - sec.VirtualAddress)
	if start+uint64(size) > uint64(len(b)) {
		return nil, fmt.Errorf("data at %#x runs past the end of section %s", rva, sec.Name)
	}
	return b[start : start+uint64(size)], nil
}
