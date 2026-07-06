// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"bytes"
	"encoding/binary"
	"errors"
	"unicode/utf16"

	"www.velocidex.com/golang/regparser"
)

// regf name flags: an nk/vk whose name is stored one byte per character (ASCII)
// rather than as UTF-16LE sets these bits.
const (
	nkCompName = 0x0020 // CM_KEY_NODE.Flags: KEY_COMP_NAME
	vkCompName = 0x0001 // CM_KEY_VALUE.Flags: VALUE_COMP_NAME
)

// ReadHive parses a regf hive image into a tree. Only the structure the writer
// reproduces is kept — key names, subkeys, and each value's name, type, and raw
// data; per-key security, class names, and timestamps are discarded.
func ReadHive(data []byte) (*Hive, error) {
	reg, err := regparser.NewRegistry(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	root := reg.OpenKey("")
	if root == nil {
		return nil, errors.New("hive: no root key")
	}
	return &Hive{Root: readKey(root)}, nil
}

// readKey converts a regparser key node and its whole subtree into our model.
func readKey(nk *regparser.CM_KEY_NODE) *Key {
	k := &Key{Name: nodeName(nk)}
	for _, v := range nk.Values() {
		k.Values = append(k.Values, readValue(v))
	}
	for _, sub := range nk.Subkeys() {
		k.Subkeys = append(k.Subkeys, readKey(sub))
	}
	return k
}

// readValue converts a regparser value node, preserving its raw on-disk bytes.
func readValue(vk *regparser.CM_KEY_VALUE) Value {
	// ValueData reassembles the bytes (including big-data segments) but pads
	// short inline data to four bytes; trim back to the real length.
	data := vk.ValueData().Data
	if n := vk.DataSize(); int64(len(data)) > n {
		data = data[:n]
	}
	return Value{Name: valueName(vk), Type: vk.Type(), Data: data}
}

// nodeName returns a key's name. regparser returns the raw name bytes as a Go
// string without consulting the compression flag, so we decode here: a
// compressed name is one byte per character (Latin-1); otherwise it is UTF-16LE.
func nodeName(nk *regparser.CM_KEY_NODE) string {
	if nk.Flags()&nkCompName != 0 {
		return decodeLatin1([]byte(nk.Name()))
	}
	return decodeUTF16([]byte(nk.Name()))
}

// valueName returns a value's name, decoding a compressed (Latin-1) or UTF-16LE
// name as nodeName does.
func valueName(vk *regparser.CM_KEY_VALUE) string {
	if vk.Flags()&vkCompName != 0 {
		return decodeLatin1([]byte(vk.ValueName()))
	}
	return decodeUTF16([]byte(vk.ValueName()))
}

// decodeLatin1 decodes one-byte-per-character (compressed) name bytes: each byte
// is the code point of a character (0x00–0xFF), so a byte such as 0xDC becomes
// U+00DC rather than an invalid UTF-8 sequence.
func decodeLatin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// decodeUTF16 decodes little-endian UTF-16 bytes to a Go string. A trailing odd
// byte (never expected in a well-formed name) is ignored.
func decodeUTF16(b []byte) string {
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, binary.LittleEndian.Uint16(b[i:]))
	}
	return string(utf16.Decode(u16))
}
