// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"encoding/binary"
	"testing"
)

func TestFileVersionAtLeast(t *testing.T) {
	tests := []struct {
		v, min fileVersion
		want   bool
	}{
		{fileVersion{5, 1, 2600, 5512}, minVersion, true},  // XP SP3 — the exact minimum
		{fileVersion{5, 1, 2600, 6000}, minVersion, true},  // above it: post-SP3 servicing
		{fileVersion{5, 1, 2600, 5511}, minVersion, false}, // one below
		{fileVersion{5, 1, 2600, 2180}, minVersion, false}, // XP SP2
		{fileVersion{5, 1, 2600, 1045}, minVersion, false}, // XP SP1
		{fileVersion{5, 1, 2600, 0}, minVersion, false},    // XP RTM
		{fileVersion{5, 2, 3790, 0}, minVersion, true},     // Server 2003
		{fileVersion{5, 0, 2195, 0}, minVersion, false},    // Windows 2000
	}
	for _, tt := range tests {
		if got := tt.v.atLeast(tt.min); got != tt.want {
			t.Errorf("%s.atLeast(%s) = %v, want %v", tt.v, tt.min, got, tt.want)
		}
	}
}

func TestParseFixedFileInfo(t *testing.T) {
	blob := versionBlob(fileVersion{5, 1, 2600, 5512})
	got, err := parseFixedFileInfo(blob)
	if err != nil {
		t.Fatal(err)
	}
	want := fileVersion{5, 1, 2600, 5512}
	if got != want {
		t.Errorf("parseFixedFileInfo = %s, want %s", got, want)
	}
}

func TestParseFixedFileInfoMissing(t *testing.T) {
	if _, err := parseFixedFileInfo([]byte("no signature here at all")); err == nil {
		t.Error("expected error for a blob without VS_FIXEDFILEINFO")
	}
}

func TestFindVersionBlob(t *testing.T) {
	const base = 0x2000
	rsrc := buildRsrc(base, versionBlob(fileVersion{6, 0, 3790, 42}))
	blob, err := findVersionBlob(rsrc, base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseFixedFileInfo(blob)
	if err != nil {
		t.Fatal(err)
	}
	want := fileVersion{6, 0, 3790, 42}
	if got != want {
		t.Errorf("version = %s, want %s", got, want)
	}
}

// versionBlob builds a minimal blob carrying a VS_FIXEDFILEINFO with the given
// FileVersion, prefixed by a few bytes so the signature is not at offset 0.
func versionBlob(v fileVersion) []byte {
	b := make([]byte, 8+16)
	copy(b, []byte("VSVI")) // filler ahead of the signature
	off := 8
	binary.LittleEndian.PutUint32(b[off:], vsFixedFileInfoSig)
	binary.LittleEndian.PutUint32(b[off+4:], 0x00010000) // dwStrucVersion
	binary.LittleEndian.PutUint32(b[off+8:], uint32(v.Major)<<16|uint32(v.Minor))
	binary.LittleEndian.PutUint32(b[off+12:], uint32(v.Build)<<16|uint32(v.Revision))
	return b
}

// buildRsrc assembles a three-level resource directory holding one RT_VERSION
// resource whose data is blob, laid out at section RVA base.
func buildRsrc(base uint32, blob []byte) []byte {
	// Fixed layout: dir(16+8) x3 = 72, data entry 16, then the blob.
	const dirSize = 16 + 8
	l1 := 0
	l2 := l1 + dirSize
	l3 := l2 + dirSize
	dataEntry := l3 + dirSize
	blobOff := dataEntry + 16

	buf := make([]byte, blobOff+len(blob))

	writeDir := func(off, id int, target uint32) {
		binary.LittleEndian.PutUint16(buf[off+12:], 0) // named entries
		binary.LittleEndian.PutUint16(buf[off+14:], 1) // id entries
		binary.LittleEndian.PutUint32(buf[off+16:], uint32(id))
		binary.LittleEndian.PutUint32(buf[off+20:], target)
	}
	writeDir(l1, rtVersion, subdirBit|uint32(l2))
	writeDir(l2, 1, subdirBit|uint32(l3))
	writeDir(l3, 0, uint32(dataEntry)) // leaf: no subdir bit

	binary.LittleEndian.PutUint32(buf[dataEntry:], base+uint32(blobOff)) // RVA
	binary.LittleEndian.PutUint32(buf[dataEntry+4:], uint32(len(blob)))  // size
	copy(buf[blobOff:], blob)
	return buf
}
