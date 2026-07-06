// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
)

// fileVersion is a four-part Windows file version (e.g. 5.1.2600.5512).
type fileVersion struct {
	Major, Minor, Build, Revision int
}

func (v fileVersion) String() string {
	return fmt.Sprintf("%d.%d.%d.%d", v.Major, v.Minor, v.Build, v.Revision)
}

// atLeast reports whether v is greater than or equal to o, compared part by part.
func (v fileVersion) atLeast(o fileVersion) bool {
	for _, d := range [][2]int{
		{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Build, o.Build}, {v.Revision, o.Revision},
	} {
		if d[0] != d[1] {
			return d[0] > d[1]
		}
	}
	return true
}

const (
	// rtVersion is the resource type ID for version resources (RT_VERSION).
	rtVersion = 16
	// vsFixedFileInfoSig marks the VS_FIXEDFILEINFO struct within a version blob.
	vsFixedFileInfoSig = 0xFEEF04BD
	// subdirBit flags a resource directory entry as pointing at a subdirectory.
	subdirBit = 0x80000000
)

// peFileVersion reads the FileVersion from a PE image's version resource. It walks
// the .rsrc resource directory to the first RT_VERSION leaf and reads the
// VS_FIXEDFILEINFO structure from the version blob.
func peFileVersion(image []byte) (fileVersion, error) {
	f, err := pe.NewFile(bytes.NewReader(image))
	if err != nil {
		return fileVersion{}, fmt.Errorf("parse PE: %w", err)
	}
	defer f.Close()

	sec := f.Section(".rsrc")
	if sec == nil {
		return fileVersion{}, fmt.Errorf("no .rsrc section")
	}
	rsrc, err := sec.Data()
	if err != nil {
		return fileVersion{}, fmt.Errorf("read .rsrc: %w", err)
	}
	// Section.Data pads to the file-aligned size; trim to the virtual size so
	// resource offsets do not run into padding.
	if n := int(sec.VirtualSize); n > 0 && n < len(rsrc) {
		rsrc = rsrc[:n]
	}

	blob, err := findVersionBlob(rsrc, sec.VirtualAddress)
	if err != nil {
		return fileVersion{}, err
	}
	return parseFixedFileInfo(blob)
}

// findVersionBlob returns the bytes of the first RT_VERSION resource. The resource
// directory has three levels (type → name/ID → language); the leaf is an
// IMAGE_RESOURCE_DATA_ENTRY whose RVA is resolved against the section's RVA base.
func findVersionBlob(rsrc []byte, base uint32) ([]byte, error) {
	typeEntry, err := resDirEntryByID(rsrc, 0, rtVersion)
	if err != nil {
		return nil, fmt.Errorf("locate RT_VERSION: %w", err)
	}
	nameEntry, err := resFirstEntry(rsrc, typeEntry, "RT_VERSION names")
	if err != nil {
		return nil, err
	}
	leaf, err := resFirstEntry(rsrc, nameEntry, "RT_VERSION languages")
	if err != nil {
		return nil, err
	}
	if leaf&subdirBit != 0 {
		return nil, fmt.Errorf("RT_VERSION language entry is not a leaf")
	}
	de := int(leaf)
	if de < 0 || de+8 > len(rsrc) {
		return nil, fmt.Errorf("resource data entry out of range")
	}
	rva := binary.LittleEndian.Uint32(rsrc[de:])
	size := binary.LittleEndian.Uint32(rsrc[de+4:])
	if rva < base {
		return nil, fmt.Errorf("version blob RVA %#x precedes section base %#x", rva, base)
	}
	off := int(rva - base)
	if off < 0 || off+int(size) > len(rsrc) {
		return nil, fmt.Errorf("version blob out of range")
	}
	return rsrc[off : off+int(size)], nil
}

// resFirstEntry returns the OffsetToData of the first entry of the subdirectory
// pointed at by parent (whose subdirectory bit must be set). label names the level
// for error messages.
func resFirstEntry(rsrc []byte, parent uint32, label string) (uint32, error) {
	if parent&subdirBit == 0 {
		return 0, fmt.Errorf("%s: parent is not a subdirectory", label)
	}
	count, entries, err := resDirEntries(rsrc, int(parent&^subdirBit))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", label, err)
	}
	if count == 0 {
		return 0, fmt.Errorf("%s: empty directory", label)
	}
	return binary.LittleEndian.Uint32(rsrc[entries+4:]), nil
}

// resDirEntryByID finds the entry with the given ID in the directory at dirOff and
// returns its OffsetToData field (high bit = subdirectory).
func resDirEntryByID(rsrc []byte, dirOff, id int) (uint32, error) {
	count, entries, err := resDirEntries(rsrc, dirOff)
	if err != nil {
		return 0, err
	}
	for i := 0; i < count; i++ {
		e := entries + i*8
		name := binary.LittleEndian.Uint32(rsrc[e:])
		// ID entries have the high bit clear; skip name entries (high bit set).
		if name&subdirBit == 0 && int(name) == id {
			return binary.LittleEndian.Uint32(rsrc[e+4:]), nil
		}
	}
	return 0, fmt.Errorf("resource id %d not found", id)
}

// resDirEntries validates the IMAGE_RESOURCE_DIRECTORY at dirOff and returns the
// total entry count and the offset of the first entry.
func resDirEntries(rsrc []byte, dirOff int) (count, entriesOff int, err error) {
	// IMAGE_RESOURCE_DIRECTORY is 16 bytes: NumberOfNamedEntries at +12,
	// NumberOfIdEntries at +14, then the entries (8 bytes each).
	if dirOff < 0 || dirOff+16 > len(rsrc) {
		return 0, 0, fmt.Errorf("resource directory out of range")
	}
	named := int(binary.LittleEndian.Uint16(rsrc[dirOff+12:]))
	ids := int(binary.LittleEndian.Uint16(rsrc[dirOff+14:]))
	total := named + ids
	entriesOff = dirOff + 16
	if total < 0 || entriesOff+total*8 > len(rsrc) {
		return 0, 0, fmt.Errorf("resource directory entries out of range")
	}
	return total, entriesOff, nil
}

// parseFixedFileInfo locates the VS_FIXEDFILEINFO signature within a version blob
// and reads dwFileVersionMS/LS.
func parseFixedFileInfo(blob []byte) (fileVersion, error) {
	var sig [4]byte
	binary.LittleEndian.PutUint32(sig[:], vsFixedFileInfoSig)
	i := bytes.Index(blob, sig[:])
	if i < 0 || i+16 > len(blob) {
		return fileVersion{}, fmt.Errorf("VS_FIXEDFILEINFO not found")
	}
	// From the signature: dwSignature, dwStrucVersion, dwFileVersionMS,
	// dwFileVersionLS, ...
	ms := binary.LittleEndian.Uint32(blob[i+8:])
	ls := binary.LittleEndian.Uint32(blob[i+12:])
	return fileVersion{
		Major:    int(ms >> 16),
		Minor:    int(ms & 0xffff),
		Build:    int(ls >> 16),
		Revision: int(ls & 0xffff),
	}, nil
}
