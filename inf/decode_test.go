// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import "testing"

// A tiny INF, plus a non-ASCII character so a code-page byte and a UTF-16 code
// unit both have to survive the round trip.
const encSample = "[Files]\nname = vàlue\n"

// utf16Bytes encodes s as UTF-16 with a leading BOM, in the given byte order.
func utf16Bytes(s string, big bool) []byte {
	var out []byte
	put := func(u uint16) {
		if big {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	put(0xFEFF) // BOM, written in the same order as the text
	for _, r := range s {
		put(uint16(r))
	}
	return out
}

func TestDecode(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"utf16le", utf16Bytes(encSample, false), encSample},
		{"utf16be", utf16Bytes(encSample, true), encSample},
		{"utf8bom", append([]byte{0xEF, 0xBB, 0xBF}, encSample...), encSample},
		{"utf8", []byte(encSample), encSample},
		// A BOM-less single-byte file is passed through byte-for-byte, whatever its
		// code page: here CP-1252 0xE0 for "à", which is not valid UTF-8.
		{"ansi", []byte("[Files]\nname = v\xe0lue\n"), "[Files]\nname = v\xe0lue\n"},
		{"empty", nil, ""},
		{"bom only", []byte{0xFF, 0xFE}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(Decode(c.in)); got != c.want {
				t.Errorf("Decode() = %q, want %q", got, c.want)
			}
		})
	}
}

// Parse decodes its input, so an INF in any of these encodings yields the same
// sections and fields — a UTF-16 file must not be read as a directive of BOM bytes
// before any section.
func TestParseEncodings(t *testing.T) {
	cases := map[string][]byte{
		"utf16le": utf16Bytes(encSample, false),
		"utf16be": utf16Bytes(encSample, true),
		"utf8bom": append([]byte{0xEF, 0xBB, 0xBF}, encSample...),
		"utf8":    []byte(encSample),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			f, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			lines := f.Section("Files", 0)
			if len(lines) != 1 {
				t.Fatalf("[Files] has %d lines, want 1", len(lines))
			}
			if lines[0].Key != "name" || lines[0].Field(0) != "vàlue" {
				t.Errorf("line = %q = %q, want %q = %q", lines[0].Key, lines[0].Field(0), "name", "vàlue")
			}
		})
	}
}
