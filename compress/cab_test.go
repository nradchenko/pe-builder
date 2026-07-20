// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package compress

import (
	"bytes"
	"encoding/binary"
	"strings"
	"sync"
	"testing"
)

// cabBuilder assembles a minimal, uncompressed (method NONE) MSCF cabinet in
// memory. It exercises the framing parser without needing a compressor: folder
// payloads are stored verbatim. Files are assigned to folders in order.
type cabBuilder struct {
	folders [][]byte // each folder's concatenated uncompressed content
	files   []cabBuildFile
}

type cabBuildFile struct {
	name   string
	folder int
	data   []byte
}

func (b *cabBuilder) addFolder(files ...cabBuildFile) {
	idx := len(b.folders)
	var content []byte
	for _, f := range files {
		f.folder = idx
		content = append(content, f.data...)
		b.files = append(b.files, f)
	}
	b.folders = append(b.folders, content)
}

func (b *cabBuilder) build() []byte {
	const (
		headerLen = 36
		folderLen = 8
		fileLen   = 16 // fixed CFFILE fields before the name
	)
	// Layout: header | folders[] | files[] | data-blocks[].
	fileEntriesLen := 0
	for _, f := range b.files {
		fileEntriesLen += fileLen + len(f.name) + 1
	}
	coffFiles := headerLen + folderLen*len(b.folders)
	dataStart := coffFiles + fileEntriesLen

	out := &bytes.Buffer{}
	// CFHEADER.
	out.WriteString("MSCF")
	writeU32(out, 0)                 // reserved1
	writeU32(out, 0)                 // cbCabinet (unused by reader)
	writeU32(out, 0)                 // reserved2
	writeU32(out, uint32(coffFiles)) // coffFiles
	writeU32(out, 0)                 // reserved3
	out.WriteByte(3)                 // versionMinor
	out.WriteByte(1)                 // versionMajor
	writeU16(out, uint16(len(b.folders)))
	writeU16(out, uint16(len(b.files)))
	writeU16(out, 0) // flags
	writeU16(out, 0) // setID
	writeU16(out, 0) // iCabinet

	// CFFOLDER entries. Compute each folder's data-block offset.
	folderDataOff := make([]int, len(b.folders))
	off := dataStart
	for i, content := range b.folders {
		folderDataOff[i] = off
		off += 8 + len(content) // one CFDATA block per folder
	}
	for i := range b.folders {
		writeU32(out, uint32(folderDataOff[i])) // coffCabStart
		writeU16(out, 1)                        // one CFDATA block
		writeU16(out, compNone)                 // typeCompress = NONE
	}

	// CFFILE entries. Recompute per-folder running offsets.
	folderRun := make([]int, len(b.folders))
	for _, f := range b.files {
		writeU32(out, uint32(len(f.data)))         // cbFile
		writeU32(out, uint32(folderRun[f.folder])) // uoffFolderStart
		writeU16(out, uint16(f.folder))            // iFolder
		writeU16(out, 0)                           // date
		writeU16(out, 0)                           // time
		writeU16(out, 0)                           // attribs
		out.WriteString(f.name)
		out.WriteByte(0)
		folderRun[f.folder] += len(f.data)
	}

	// CFDATA blocks (one per folder, stored).
	for _, content := range b.folders {
		writeU32(out, 0)                    // csum
		writeU16(out, uint16(len(content))) // cbData
		writeU16(out, uint16(len(content))) // cbUncomp
		out.Write(content)
	}
	return out.Bytes()
}

// fingerprintOf returns the fingerprint of a cabinet's directory region, the way
// the extraction stages compute it (over head[:CabinetDirSize]).
func fingerprintOf(t *testing.T, cab []byte) string {
	t.Helper()
	n, err := CabinetDirSize(cab)
	if err != nil {
		t.Fatalf("CabinetDirSize: %v", err)
	}
	return Fingerprint(cab[:n])
}

// TestFingerprintInvalidates documents what the extracted-member cache keys off:
// a fingerprint of the cabinet's directory region (names, sizes, offsets, folder
// layout, header). Any change a real source cabinet would carry — a member added,
// resized, renamed, or moved — changes the fingerprint, so a stale member is never
// served after the source changes.
func TestFingerprintInvalidates(t *testing.T) {
	base := func() []byte {
		b := &cabBuilder{}
		b.addFolder(
			cabBuildFile{name: "mouhid.sys", data: bytes.Repeat([]byte{0x11}, 400)},
			cabBuildFile{name: "mouclass.sys", data: bytes.Repeat([]byte{0x22}, 700)},
		)
		return b.build()
	}
	baseFP := fingerprintOf(t, base())

	// Identical bytes → identical fingerprint (so the same source reuses its cache).
	if got := fingerprintOf(t, base()); got != baseFP {
		t.Errorf("identical cabinet: fingerprint changed\n got %s\nwant %s", got, baseFP)
	}

	cases := []struct {
		name  string
		build func() []byte
	}{
		{"resized member", func() []byte {
			b := &cabBuilder{}
			b.addFolder(
				cabBuildFile{name: "mouhid.sys", data: bytes.Repeat([]byte{0x11}, 401)}, // 400 -> 401
				cabBuildFile{name: "mouclass.sys", data: bytes.Repeat([]byte{0x22}, 700)},
			)
			return b.build()
		}},
		{"renamed member", func() []byte {
			b := &cabBuilder{}
			b.addFolder(
				cabBuildFile{name: "mouhid2.sys", data: bytes.Repeat([]byte{0x11}, 400)},
				cabBuildFile{name: "mouclass.sys", data: bytes.Repeat([]byte{0x22}, 700)},
			)
			return b.build()
		}},
		{"added member", func() []byte {
			b := &cabBuilder{}
			b.addFolder(
				cabBuildFile{name: "mouhid.sys", data: bytes.Repeat([]byte{0x11}, 400)},
				cabBuildFile{name: "mouclass.sys", data: bytes.Repeat([]byte{0x22}, 700)},
				cabBuildFile{name: "kbdhid.sys", data: bytes.Repeat([]byte{0x33}, 200)},
			)
			return b.build()
		}},
	}
	for _, tc := range cases {
		if got := fingerprintOf(t, tc.build()); got == baseFP {
			t.Errorf("%s: fingerprint did not change (%s) — a changed cabinet would serve stale members", tc.name, got)
		}
	}
}

// TestFingerprintPayloadOnlyLimitation records the one change the directory
// fingerprint does NOT detect: a member's payload edited to the exact same length
// with identical CFFILE metadata (name, size, offset, date). This is not a
// real-source scenario — a rebuilt or patched cabinet changes member sizes and
// timestamps — but it is a known bound of a metadata-only fingerprint, pinned here
// so a future change to the keying is a deliberate decision.
func TestFingerprintPayloadOnlyLimitation(t *testing.T) {
	mk := func(payload byte) []byte {
		b := &cabBuilder{}
		b.addFolder(cabBuildFile{name: "mod.sys", data: bytes.Repeat([]byte{payload}, 400)})
		return b.build()
	}
	if fingerprintOf(t, mk(0xAA)) != fingerprintOf(t, mk(0xBB)) {
		t.Skip("payload-only difference now changes the fingerprint; the cache is stricter than documented")
	}
}

func TestCabinetSingleFolderMultiFile(t *testing.T) {
	b := &cabBuilder{}
	b.addFolder(
		cabBuildFile{name: "alpha.txt", data: []byte("the first file")},
		cabBuildFile{name: "beta.bin", data: bytes.Repeat([]byte{0xAB}, 5000)},
	)
	cab, err := ReadCabinet(b.build())
	if err != nil {
		t.Fatal(err)
	}
	if len(cab.Files()) != 2 {
		t.Fatalf("files = %d, want 2", len(cab.Files()))
	}
	for _, want := range b.files {
		f, ok := cab.Find(want.name)
		if !ok {
			t.Errorf("%s: not found", want.name)
			continue
		}
		got, err := cab.Data(f)
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if !bytes.Equal(got, want.data) {
			t.Errorf("%s: content mismatch", want.name)
		}
	}
}

func TestCabinetMultiFolderRouting(t *testing.T) {
	b := &cabBuilder{}
	b.addFolder(cabBuildFile{name: "in-folder-0.txt", data: []byte("zero")})
	b.addFolder(
		cabBuildFile{name: "in-folder-1a.txt", data: []byte("one-a")},
		cabBuildFile{name: "in-folder-1b.txt", data: []byte("one-b-longer")},
	)
	b.addFolder(cabBuildFile{name: "in-folder-2.txt", data: bytes.Repeat([]byte("2"), 100)})

	cab, err := ReadCabinet(b.build())
	if err != nil {
		t.Fatal(err)
	}
	if len(cab.folders) != 3 {
		t.Fatalf("folders = %d, want 3", len(cab.folders))
	}
	// Each file must resolve to the content stored in its own folder, proving
	// the iFolder/uoffFolderStart routing.
	for _, want := range b.files {
		f, _ := cab.Find(want.name)
		got, err := cab.Data(f)
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if !bytes.Equal(got, want.data) {
			t.Errorf("%s: got %q, want %q", want.name, got, want.data)
		}
	}
}

func TestCabinetDirSizeIndexesWithoutData(t *testing.T) {
	b := &cabBuilder{}
	b.addFolder(
		cabBuildFile{name: "alpha.txt", data: []byte("the first file")},
		cabBuildFile{name: "beta.bin", data: bytes.Repeat([]byte{0xAB}, 50000)},
	)
	b.addFolder(cabBuildFile{name: "gamma.dat", data: bytes.Repeat([]byte("g"), 20000)})
	full := b.build()

	dirLen, err := CabinetDirSize(full)
	if err != nil {
		t.Fatalf("CabinetDirSize: %v", err)
	}
	// The directory must be a small fraction of the whole: the 70 KB of payload is
	// excluded, which is the entire point of indexing from the prefix.
	if dirLen >= len(full) {
		t.Fatalf("dirLen = %d, want well under the full %d bytes", dirLen, len(full))
	}

	// Parsing only the directory prefix must enumerate exactly the same members as
	// parsing the whole cabinet — proving members can be indexed without the data.
	dirOnly, err := ReadCabinet(full[:dirLen])
	if err != nil {
		t.Fatalf("ReadCabinet(prefix): %v", err)
	}
	full2, err := ReadCabinet(full)
	if err != nil {
		t.Fatalf("ReadCabinet(full): %v", err)
	}
	if got, want := len(dirOnly.Files()), len(full2.Files()); got != want {
		t.Fatalf("prefix files = %d, full files = %d", got, want)
	}
	for _, f := range full2.Files() {
		if _, ok := dirOnly.Find(f.Name); !ok {
			t.Errorf("member %q missing from directory-only parse", f.Name)
		}
	}
}

func TestCabinetDirSizeRejectsNonCabinet(t *testing.T) {
	if _, err := CabinetDirSize([]byte("NOPE just some bytes, not a cabinet")); err == nil {
		t.Error("expected an error for non-cabinet input")
	}
	// A header that ends before the folder table is short — must error, not panic.
	if _, err := CabinetDirSize([]byte("MSCF\x00\x00\x00\x00")); err == nil {
		t.Error("expected an error for a truncated header")
	}
}

func TestCabinetReserveFields(t *testing.T) {
	// A cabinet with RESERVE_PRESENT and non-zero per-folder and per-data
	// reserve sizes must still parse: the reserved bytes are skipped.
	base := (&cabBuilder{}).withReserve(4, 2, 3)
	base.addFolder(cabBuildFile{name: "r.txt", data: []byte("reserved-aware")})
	cab, err := ReadCabinet(base.build())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := cab.Find("r.txt")
	if !ok {
		t.Fatal("file not found")
	}
	got, err := cab.Data(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("reserved-aware"); !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCabinetConcurrentExtraction(t *testing.T) {
	// Extract every member from many goroutines at once. Run with -race; before
	// the cache was guarded this raced (and could hit "concurrent map writes").
	cab, err := ReadCabinet(readFixture(t, "lzx-multi.cab"))
	if err != nil {
		t.Fatal(err)
	}
	files := cab.Files()
	want := make([][]byte, len(files))
	for i, f := range files {
		if want[i], err = cab.Data(f); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, f := range files {
				got, err := cab.Data(f)
				if err != nil {
					t.Errorf("%s: %v", f.Name, err)
					return
				}
				if !bytes.Equal(got, want[i]) {
					t.Errorf("%s: concurrent extraction mismatch", f.Name)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestIsCabinet(t *testing.T) {
	if !IsCabinet([]byte("MSCF\x00\x00")) {
		t.Error("MSCF prefix should be recognised")
	}
	if IsCabinet([]byte("MSZ")) || IsCabinet([]byte("PK\x03\x04")) {
		t.Error("non-MSCF data must not be recognised")
	}
}

func TestCabinetTruncated(t *testing.T) {
	full := func() []byte {
		b := &cabBuilder{}
		b.addFolder(cabBuildFile{name: "x", data: []byte("payload")})
		return b.build()
	}()
	// Truncating anywhere must yield an error, never a panic. Header/folder/file
	// framing is parsed by ReadCabinet; the CFDATA payload is read lazily, so a
	// cut in the trailing data region surfaces only at extraction time.
	for n := 1; n < len(full); n += 3 {
		cab, err := ReadCabinet(full[:n])
		if err != nil {
			continue
		}
		errored := false
		for _, f := range cab.Files() {
			if _, err := cab.Data(f); err != nil {
				errored = true
			}
		}
		if !errored {
			t.Errorf("truncation at len=%d produced no error at either stage", n)
		}
	}
}

func TestCabinetBadMagic(t *testing.T) {
	if _, err := ReadCabinet([]byte("NOPE................................")); err == nil {
		t.Error("expected error for bad magic")
	}
}

func TestCabinetUnsupportedMethod(t *testing.T) {
	b := &cabBuilder{}
	b.addFolder(cabBuildFile{name: "m.txt", data: []byte("data")})
	raw := b.build()
	// Patch folder 0's typeCompress to Quantum (method 2) and expect a clear
	// error. Quantum is the one method this package still does not decode; NONE,
	// MSZIP, and LZX are all handled. typeCompress sits at header(36) + 6 within
	// the first CFFOLDER.
	binary.LittleEndian.PutUint16(raw[36+6:], compQuantum)
	cab, err := ReadCabinet(raw)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := cab.Find("m.txt")
	if _, err := cab.Data(f); err == nil || !strings.Contains(err.Error(), "Quantum") {
		t.Fatalf("want Quantum-unsupported error, got %v", err)
	}
}

// withReserve rebuilds the builder to emit reserve fields. Implemented as a
// small variant of build() to keep the common path simple.
func (b *cabBuilder) withReserve(hdr, folder, data int) *reserveBuilder {
	return &reserveBuilder{cabBuilder: b, hdrReserve: hdr, folderReserve: folder, dataReserve: data}
}

type reserveBuilder struct {
	*cabBuilder
	hdrReserve, folderReserve, dataReserve int
}

func (b *reserveBuilder) build() []byte {
	const fileLen = 16
	headerLen := 36 + 4 + b.hdrReserve // + cbCFHeader/cbCFFolder/cbCFData + abReserve
	folderLen := 8 + b.folderReserve
	fileEntriesLen := 0
	for _, f := range b.files {
		fileEntriesLen += fileLen + len(f.name) + 1
	}
	coffFiles := headerLen + folderLen*len(b.folders)
	dataStart := coffFiles + fileEntriesLen

	out := &bytes.Buffer{}
	out.WriteString("MSCF")
	writeU32(out, 0)
	writeU32(out, 0)
	writeU32(out, 0)
	writeU32(out, uint32(coffFiles))
	writeU32(out, 0)
	out.WriteByte(3)
	out.WriteByte(1)
	writeU16(out, uint16(len(b.folders)))
	writeU16(out, uint16(len(b.files)))
	writeU16(out, cfhdrReservePresent)
	writeU16(out, 0)
	writeU16(out, 0)
	// Reserve descriptor + abReserve.
	writeU16(out, uint16(b.hdrReserve))
	out.WriteByte(byte(b.folderReserve))
	out.WriteByte(byte(b.dataReserve))
	out.Write(make([]byte, b.hdrReserve))

	folderDataOff := make([]int, len(b.folders))
	off := dataStart
	for i, content := range b.folders {
		folderDataOff[i] = off
		off += 8 + b.dataReserve + len(content)
	}
	for i := range b.folders {
		writeU32(out, uint32(folderDataOff[i]))
		writeU16(out, 1)
		writeU16(out, compNone)
		out.Write(make([]byte, b.folderReserve))
	}
	folderRun := make([]int, len(b.folders))
	for _, f := range b.files {
		writeU32(out, uint32(len(f.data)))
		writeU32(out, uint32(folderRun[f.folder]))
		writeU16(out, uint16(f.folder))
		writeU16(out, 0)
		writeU16(out, 0)
		writeU16(out, 0)
		out.WriteString(f.name)
		out.WriteByte(0)
		folderRun[f.folder] += len(f.data)
	}
	for _, content := range b.folders {
		writeU32(out, 0)
		writeU16(out, uint16(len(content)))
		writeU16(out, uint16(len(content)))
		out.Write(make([]byte, b.dataReserve))
		out.Write(content)
	}
	return out.Bytes()
}

func writeU16(b *bytes.Buffer, v uint16) {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], v)
	b.Write(buf[:])
}

func writeU32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}
