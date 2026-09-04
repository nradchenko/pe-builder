// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// TestNameHint pins the fast-leaf hint rules: four bytes of the stored name, zero-padded,
// case kept, cut off at the first character that has no single-byte form.
func TestNameHint(t *testing.T) {
	utf16le := func(s string) []byte {
		u := utf16.Encode([]rune(s))
		b := make([]byte, 2*len(u))
		for i, c := range u {
			binary.LittleEndian.PutUint16(b[2*i:], c)
		}
		return b
	}
	cases := []struct {
		name       string
		bytes      []byte
		compressed bool
		want       [4]byte
	}{
		{"compressed, long", []byte("Services"), true, [4]byte{'S', 'e', 'r', 'v'}},
		{"compressed, short", []byte("ab"), true, [4]byte{'a', 'b', 0, 0}},
		{"compressed, exact", []byte("0000"), true, [4]byte{'0', '0', '0', '0'}},
		{"compressed, case kept", []byte("aBcD"), true, [4]byte{'a', 'B', 'c', 'D'}},
		{"utf16, latin-1 byte", utf16le("état"), false, [4]byte{0xE9, 't', 'a', 't'}},
		{"utf16, wide char stops the hint", utf16le("aЖbc"), false, [4]byte{'a', 0, 0, 0}},
		{"utf16, short", utf16le("x"), false, [4]byte{'x', 0, 0, 0}},
		{"empty", nil, true, [4]byte{}},
	}
	for _, c := range cases {
		if got := nameHint(c.bytes, c.compressed); got != c.want {
			t.Errorf("%s: nameHint = %q, want %q", c.name, got[:], c.want[:])
		}
	}
}

// TestWriteEmitsFastLeaves walks every allocated cell of a written hive and checks that
// each subkey index is a fast leaf (lf) — the form Windows 2000 reads — and that no hash
// leaf (lh) is left; each lf element's hint must be the hint of the subkey it points at.
// Windows 2000's loader rejects a hive whose indexes are lh with "\WINNT\SYSTEM32\CONFIG\
// SYSTEM is missing or corrupt", which no host-side reader reports.
func TestWriteEmitsFastLeaves(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	var lf, lh int
	binsLen := binary.LittleEndian.Uint32(data[0x28:])
	for off := uint32(baseBlockSize); off < baseBlockSize+binsLen; {
		if string(data[off:off+4]) != "hbin" {
			t.Fatalf("no hbin signature at %#x", off)
		}
		binSize := binary.LittleEndian.Uint32(data[off+8:])
		for c := off + 0x20; c < off+binSize; {
			size := int32(binary.LittleEndian.Uint32(data[c:]))
			if size == 0 {
				t.Fatalf("zero-size cell at %#x", c)
			}
			if size < 0 { // allocated
				switch string(data[c+4 : c+6]) {
				case "lh":
					lh++
				case "lf":
					lf++
					n := int(binary.LittleEndian.Uint16(data[c+6:]))
					for i := 0; i < n; i++ {
						el := c + 8 + uint32(8*i)
						nk := baseBlockSize + binary.LittleEndian.Uint32(data[el:]) + 4
						if string(data[nk:nk+2]) != "nk" {
							t.Fatalf("lf element %d at %#x points at a non-nk cell", i, el)
						}
						nameLen := binary.LittleEndian.Uint16(data[nk+0x48:])
						compressed := binary.LittleEndian.Uint16(data[nk+2:])&0x0020 != 0
						want := nameHint(data[nk+nkFixedLen:nk+nkFixedLen+uint32(nameLen)], compressed)
						var got [4]byte
						copy(got[:], data[el+4:])
						if got != want {
							t.Errorf("lf element at %#x: hint %q, want %q", el, got[:], want[:])
						}
					}
				}
				size = -size
			}
			c += uint32(size)
		}
		off += binSize
	}
	if lh != 0 {
		t.Errorf("written hive has %d lh (hash leaf) cells, want 0", lh)
	}
	if lf == 0 {
		t.Errorf("written hive has no lf (fast leaf) cells, want at least one for the sample tree's subkeys")
	}
}
