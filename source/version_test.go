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
