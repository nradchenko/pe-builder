// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package peresource

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// res describes one resource to lay into a test image: how it is identified, and its
// languages.
type res struct {
	id    int    // the resource's number, when name is empty
	name  string // the resource's name, when it is named rather than numbered
	langs []lang
}

// lang is one leaf of a resource.
type lang struct {
	id   int
	data []byte
}

// TestReadFindsEachManifestWithItsID is the shape the whole package exists for. A
// binary carrying a stub at id 1 and its real manifest at another id is the case that
// decides how the loader treats it, and reporting only one of the two — or neither —
// is the failure this replaces.
func TestReadFindsEachManifestWithItsID(t *testing.T) {
	stub := []byte(" ")
	actual := []byte(`<assembly manifestVersion="1.0"/>`)
	image := buildPE(t, TypeManifest, []res{
		{id: CreateProcessManifest, langs: []lang{{id: 1033, data: stub}}},
		{id: 123, langs: []lang{{id: 1033, data: actual}}},
	})

	got, err := Read(image, TypeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d manifests, want 2: %v", len(got), got)
	}
	if got[0].ID != 1 || string(got[0].Data) != string(stub) {
		t.Errorf("first = id %d %q, want id 1 %q", got[0].ID, got[0].Data, stub)
	}
	if got[1].ID != 123 || string(got[1].Data) != string(actual) {
		t.Errorf("second = id %d %q, want id 123 %q", got[1].ID, got[1].Data, actual)
	}
	for _, e := range got {
		if e.Lang != 1033 {
			t.Errorf("%s: language = %d, want 1033", e, e.Lang)
		}
		if e.Named() {
			t.Errorf("%s: reported as named, but it is numbered", e)
		}
	}
}

// A resource may be identified by a name instead of a number, and the two are read
// from the same field — so a reader that assumes numbers turns a name offset into a
// meaningless id.
func TestReadNamedResource(t *testing.T) {
	image := buildPE(t, TypeManifest, []res{
		{name: "SIDEBYSIDE", langs: []lang{{id: 0, data: []byte("<assembly/>")}}},
	})
	got, err := Read(image, TypeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d resources, want 1", len(got))
	}
	if !got[0].Named() || got[0].Name != "SIDEBYSIDE" {
		t.Errorf("resource = %+v, want the name SIDEBYSIDE", got[0])
	}
	if got[0].ID != 0 {
		t.Errorf("ID = %d, want 0 — a named resource has no number to report", got[0].ID)
	}
}

// One resource, several languages: each leaf is its own entry, so a caller comparing a
// localized binary against another sees both rather than whichever came first.
func TestReadReturnsEveryLanguage(t *testing.T) {
	image := buildPE(t, TypeManifest, []res{
		{id: 1, langs: []lang{{id: 1033, data: []byte("<a/>")}, {id: 1049, data: []byte("<b/>")}}},
	})
	got, err := Read(image, TypeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d leaves, want 2", len(got))
	}
	if got[0].Lang != 1033 || got[1].Lang != 1049 {
		t.Errorf("languages = %d, %d; want 1033, 1049", got[0].Lang, got[1].Lang)
	}
}

// A binary that carries resources but none of the requested type is an answer, not a
// failure — "no manifest" is the most common true result this is asked for.
func TestReadAbsentTypeIsEmptyNotAnError(t *testing.T) {
	image := buildPE(t, TypeVersion, []res{
		{id: 1, langs: []lang{{id: 1033, data: []byte("version blob")}}},
	})
	got, err := Read(image, TypeManifest)
	if err != nil {
		t.Fatalf("a binary with no manifest returned an error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want no entries", got)
	}
}

// The same, for a binary with no resource directory at all.
func TestReadNoResourceDirectory(t *testing.T) {
	image := buildPE(t, 0, nil)
	got, err := Read(image, TypeManifest)
	if err != nil {
		t.Fatalf("a binary with no resources returned an error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want no entries", got)
	}
}

// Anything that is not a PE has to be refused rather than read as one holding nothing:
// the two answers look identical to a caller and mean opposite things.
func TestReadNonPE(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"text", []byte("this is not a PE file at all")},
		{"truncated after the signature", []byte("MZ\x00\x00")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Read(tc.data, TypeManifest); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// A directory whose entry count runs past the data it sits in must be refused, not
// read into whatever follows. This is the failure mode a hand-rolled parser has
// already produced here, so it is checked rather than assumed.
func TestReadTruncatedDirectory(t *testing.T) {
	image := buildPE(t, TypeManifest, []res{
		{id: 1, langs: []lang{{id: 1033, data: []byte("<a/>")}}},
	})
	// Claim 4096 entries in the level-1 directory, which no image this size can hold.
	// That directory starts where the section's data does; NumberOfIdEntries is at +14.
	binary.LittleEndian.PutUint16(image[testHeadersSize+14:], 4096)

	_, err := Read(image, TypeManifest)
	if err == nil {
		t.Fatal("a directory claiming more entries than the image holds was accepted")
	}
	if !strings.Contains(err.Error(), "past") {
		t.Errorf("error = %q, want it to say the entries run past the data", err)
	}
}

// A directory offset near the top of the 31-bit field must be refused, not indexed.
//
// Written as off+dirHeaderSize > len(rsrc), the bounds check wraps negative on a 32-bit
// build and lets exactly this through, and the read that follows panics on a negative
// index. It passes on amd64 either way, which is why the arithmetic is done in uint64
// rather than left to look correct on the machine it happens to be tested on.
func TestReadHugeDirectoryOffset(t *testing.T) {
	image := buildPE(t, TypeManifest, []res{
		{id: 1, langs: []lang{{id: 1033, data: []byte("<a/>")}}},
	})
	// The level-1 entry sits after the directory header; its OffsetToData is at +4.
	at := testHeadersSize + dirHeaderSize + 4
	binary.LittleEndian.PutUint32(image[at:], subdirBit|0x7ffffff0)

	if _, err := Read(image, TypeManifest); err == nil {
		t.Error("a directory offset of 0x7ffffff0 was accepted")
	}
}

// ReadFirst stops at the first resource and does not read what follows. That is what
// keeps a source's identity probe answerable: it wants one version block, and must not
// be turned away by some later resource in the same binary being malformed.
func TestReadFirstStopsBeforeALaterBadResource(t *testing.T) {
	image := buildPE(t, TypeVersion, []res{
		{id: 1, langs: []lang{{id: 1033, data: []byte("the good one")}}},
		{id: 2, langs: []lang{{id: 1033, data: []byte("later")}}},
	})
	// Point the second resource's language entry at a data entry past the end of the
	// resource data, which is what a malformed later leaf looks like.
	breakSecondLeaf(t, image)

	if _, err := Read(image, TypeVersion); err == nil {
		t.Fatal("Read accepted a malformed later resource; the case under test is not set up")
	}
	got, ok, err := ReadFirst(image, TypeVersion)
	if err != nil {
		t.Fatalf("ReadFirst was stopped by a resource after the one it wanted: %v", err)
	}
	if !ok || string(got.Data) != "the good one" {
		t.Errorf("ReadFirst = %q, ok=%v; want the first resource", got.Data, ok)
	}
}

// A type the binary does not carry is reported as absent rather than as an error.
func TestReadFirstAbsent(t *testing.T) {
	image := buildPE(t, TypeVersion, []res{{id: 1, langs: []lang{{id: 0, data: []byte("v")}}}})
	if _, ok, err := ReadFirst(image, TypeManifest); err != nil || ok {
		t.Errorf("ReadFirst = ok %v, err %v; want absent and no error", ok, err)
	}
}

// Each level is bounded against the data it sits in, but not against the others, so a
// directory whose level-2 entries all point at one large level-3 directory yields their
// product. A megabyte of resources arranged that way is ten billion leaves; it has to
// be a refusal rather than a hang, because these binaries come off user-supplied media.
func TestReadCapsTheLeafCount(t *testing.T) {
	// 300 resources sharing one 300-language directory: 90,000 leaves, above the cap.
	langs := make([]lang, 300)
	for i := range langs {
		langs[i] = lang{id: i, data: []byte("x")}
	}
	resources := make([]res, 300)
	for i := range resources {
		resources[i] = res{id: i + 1, langs: langs}
	}
	got, err := Read(buildPE(t, TypeManifest, resources), TypeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxLeaves {
		t.Errorf("returned %d leaves, want no more than %d", len(got), maxLeaves)
	}
}

// Entry.String names a resource by whichever of id or name it has, since that is what
// the errors above and the command's listing are read by.
func TestEntryString(t *testing.T) {
	if got := (Entry{ID: 123}).String(); got != "id 123" {
		t.Errorf("String() = %q, want %q", got, "id 123")
	}
	if got := (Entry{Name: "SIDEBYSIDE"}).String(); got != `"SIDEBYSIDE"` {
		t.Errorf("String() = %q, want %q", got, `"SIDEBYSIDE"`)
	}
}

// breakSecondLeaf points the second resource's data entry at an RVA in no section,
// which is what a malformed leaf looks like from the reader's side.
//
// It recomputes the layout buildResourceDir produced. Should the two ever drift apart,
// the caller's precondition — that Read refuses this image — fails and says so, rather
// than the test quietly passing against an image that was never corrupted.
func breakSecondLeaf(t *testing.T, image []byte) {
	t.Helper()
	// Level 1, then level 2 with its two entries, then one level-3 directory per
	// resource, then the data entries: the second is one entry past the first.
	const resources, langsEach = 2, 1
	off := dirHeaderSize + dirEntrySize // level 1
	off += dirHeaderSize + resources*dirEntrySize
	off += resources * (dirHeaderSize + langsEach*dirEntrySize)
	second := testHeadersSize + off + dataEntrySize
	binary.LittleEndian.PutUint32(image[second:], 0xf0000000)
}

// --- test image construction ---

// PE layout constants for the image the tests build: a single .rsrc section, mapped at
// a page boundary, with headers in the first file page.
const (
	testSectionRVA  = 0x1000
	testHeadersSize = 0x200
	testCOFFOffset  = 0x80 // e_lfanew: where the PE signature goes
)

// buildPE lays out a minimal but genuine PE32 holding one .rsrc section with the given
// resources of one type. It is built rather than committed so the test carries no
// binary, and so the exact shape each case needs can be described in the case itself.
//
// Passing no resources produces an image whose resource data directory is empty, which
// is how a binary with no resources at all is represented.
func buildPE(t *testing.T, typ int, resources []res) []byte {
	t.Helper()

	rsrc := buildResourceDir(t, typ, resources, testSectionRVA)
	image := make([]byte, testHeadersSize+align(len(rsrc), 0x200))

	// MS-DOS header: the magic, and where the PE header starts.
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[0x3c:], testCOFFOffset)

	// PE signature and COFF header.
	o := testCOFFOffset
	copy(image[o:], "PE\x00\x00")
	coff := o + 4
	binary.LittleEndian.PutUint16(image[coff:], 0x14c)   // Machine: i386
	binary.LittleEndian.PutUint16(image[coff+2:], 1)     // NumberOfSections
	binary.LittleEndian.PutUint16(image[coff+16:], 0xe0) // SizeOfOptionalHeader
	binary.LittleEndian.PutUint16(image[coff+18:], 0x102)

	// Optional header: enough of one for debug/pe to accept it, plus the resource
	// data directory, which is what this package reads.
	opt := coff + 20
	binary.LittleEndian.PutUint16(image[opt:], 0x10b)       // Magic: PE32
	binary.LittleEndian.PutUint32(image[opt+28:], 0x1000)   // BaseOfCode
	binary.LittleEndian.PutUint32(image[opt+52:], 0x400000) // ImageBase
	binary.LittleEndian.PutUint32(image[opt+56:], 0x1000)   // SectionAlignment
	binary.LittleEndian.PutUint32(image[opt+60:], 0x200)    // FileAlignment
	binary.LittleEndian.PutUint32(image[opt+92:], 16)       // NumberOfRvaAndSizes
	dataDir := opt + 96                                     // the 16 directories
	if len(resources) > 0 {
		binary.LittleEndian.PutUint32(image[dataDir+2*8:], testSectionRVA)
		binary.LittleEndian.PutUint32(image[dataDir+2*8+4:], uint32(len(rsrc)))
	}

	// The one section header.
	sec := opt + 0xe0
	copy(image[sec:], ".rsrc\x00\x00\x00")
	binary.LittleEndian.PutUint32(image[sec+8:], uint32(len(rsrc))) // VirtualSize
	binary.LittleEndian.PutUint32(image[sec+12:], testSectionRVA)   // VirtualAddress
	binary.LittleEndian.PutUint32(image[sec+16:], uint32(align(len(rsrc), 0x200)))
	binary.LittleEndian.PutUint32(image[sec+20:], testHeadersSize) // PointerToRawData
	binary.LittleEndian.PutUint32(image[sec+36:], 0x40000040)      // initialized, read

	copy(image[testHeadersSize:], rsrc)
	return image
}

// buildResourceDir lays out the three-level directory: one type, then each resource,
// then each of its languages, followed by the data entries and the data itself.
func buildResourceDir(t *testing.T, typ int, resources []res, base uint32) []byte {
	t.Helper()
	if len(resources) == 0 {
		return nil
	}

	// Names are pooled first so their offsets are known before the entries that
	// reference them are written.
	var buf []byte
	nameOffsets := make(map[string]int)
	// Level 1 (one type entry) sits at 0. Level 2 follows, then one level-3 directory
	// per resource, then the data entries, then the names and the data.
	l1 := 0
	l2 := dirHeaderSize + dirEntrySize
	off := l2 + dirHeaderSize + len(resources)*dirEntrySize
	l3 := make([]int, len(resources))
	for i, r := range resources {
		l3[i] = off
		off += dirHeaderSize + len(r.langs)*dirEntrySize
	}
	dataEntries := off
	total := 0
	for _, r := range resources {
		total += len(r.langs)
	}
	off += total * dataEntrySize

	// Names, then payloads.
	for _, r := range resources {
		if r.name != "" {
			nameOffsets[r.name] = off
			off += 2 + len(r.name)*2
		}
	}
	payloadAt := make([][]int, len(resources))
	for i, r := range resources {
		payloadAt[i] = make([]int, len(r.langs))
		for j, l := range r.langs {
			payloadAt[i][j] = off
			off += len(l.data)
		}
	}
	buf = make([]byte, off)

	writeHeader := func(at, named, ids int) {
		binary.LittleEndian.PutUint16(buf[at+12:], uint16(named))
		binary.LittleEndian.PutUint16(buf[at+14:], uint16(ids))
	}
	writeEntry := func(at int, name uint32, target uint32) {
		binary.LittleEndian.PutUint32(buf[at:], name)
		binary.LittleEndian.PutUint32(buf[at+4:], target)
	}

	// Level 1: the single type.
	writeHeader(l1, 0, 1)
	writeEntry(l1+dirHeaderSize, uint32(typ), subdirBit|uint32(l2))

	// Level 2: each resource, named entries first as the format requires.
	named := 0
	for _, r := range resources {
		if r.name != "" {
			named++
		}
	}
	writeHeader(l2, named, len(resources)-named)
	for i, r := range resources {
		at := l2 + dirHeaderSize + i*dirEntrySize
		if r.name != "" {
			writeEntry(at, subdirBit|uint32(nameOffsets[r.name]), subdirBit|uint32(l3[i]))
			continue
		}
		writeEntry(at, uint32(r.id), subdirBit|uint32(l3[i]))
	}

	// Level 3: each language, pointing at its data entry.
	entry := dataEntries
	for i, r := range resources {
		writeHeader(l3[i], 0, len(r.langs))
		for j, l := range r.langs {
			writeEntry(l3[i]+dirHeaderSize+j*dirEntrySize, uint32(l.id), uint32(entry))
			binary.LittleEndian.PutUint32(buf[entry:], base+uint32(payloadAt[i][j]))
			binary.LittleEndian.PutUint32(buf[entry+4:], uint32(len(l.data)))
			entry += dataEntrySize
		}
	}

	// The names and the payloads themselves.
	for _, r := range resources {
		if r.name == "" {
			continue
		}
		at := nameOffsets[r.name]
		u := utf16.Encode([]rune(r.name))
		binary.LittleEndian.PutUint16(buf[at:], uint16(len(u)))
		for k, c := range u {
			binary.LittleEndian.PutUint16(buf[at+2+k*2:], c)
		}
	}
	for i, r := range resources {
		for j, l := range r.langs {
			copy(buf[payloadAt[i][j]:], l.data)
		}
	}
	return buf
}

// align rounds n up to a multiple of a.
func align(n, a int) int {
	if n%a == 0 {
		return n
	}
	return n + a - n%a
}
