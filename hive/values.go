// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// REG_* value types (the on-disk type field of a value).
const (
	RegNone     uint32 = 0
	RegSZ       uint32 = 1
	RegExpandSZ uint32 = 2
	RegBinary   uint32 = 3
	RegDWord    uint32 = 4
	RegMultiSZ  uint32 = 7
	RegQWord    uint32 = 11
)

// EncodeSZ encodes a string as a REG_SZ/REG_EXPAND_SZ payload: UTF-16LE with a
// trailing NUL character.
func EncodeSZ(s string) []byte {
	u16 := utf16.Encode([]rune(s))
	out := make([]byte, 2*len(u16)+2)
	for i, c := range u16 {
		binary.LittleEndian.PutUint16(out[2*i:], c)
	}
	// The final two bytes are already zero: the NUL terminator.
	return out
}

// EncodeMultiSZ encodes a list of strings as a REG_MULTI_SZ payload: each string
// as UTF-16LE with a NUL, then a final NUL to terminate the list. An empty list
// encodes as a single NUL character.
func EncodeMultiSZ(items []string) []byte {
	var out []byte
	for _, s := range items {
		out = append(out, EncodeSZ(s)...)
	}
	out = append(out, 0, 0) // list terminator
	return out
}

// EncodeDWord encodes a 32-bit value as a REG_DWORD payload: four little-endian
// bytes.
func EncodeDWord(v uint32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, v)
	return out
}

// AsString decodes a REG_SZ/REG_EXPAND_SZ value's data (UTF-16LE), dropping a
// trailing NUL. It is a convenience for spot-checks and does not validate type.
func (v Value) AsString() string {
	s := decodeUTF16(v.Data)
	if n := len(s); n > 0 && s[n-1] == 0 {
		s = s[:n-1]
	}
	return s
}

// AsDWord decodes a REG_DWORD value's four little-endian data bytes.
func (v Value) AsDWord() uint32 {
	if len(v.Data) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(v.Data)
}

// AsQWord decodes a REG_QWORD value's eight little-endian data bytes.
func (v Value) AsQWord() uint64 {
	if len(v.Data) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(v.Data)
}

// AsMultiSZ decodes a REG_MULTI_SZ value's data into its list of strings, dropping
// the terminating empty string. Like AsString it is a convenience for spot-checks
// and does not validate type. An empty or NUL-only payload decodes to no strings.
func (v Value) AsMultiSZ() []string {
	s := decodeUTF16(v.Data)
	var out []string
	for _, item := range strings.Split(s, "\x00") {
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}
