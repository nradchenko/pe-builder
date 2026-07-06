// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"sort"
	"unicode/utf16"
)

// encodeName encodes a key or value name for storage in an nk/vk record. A name
// whose characters all fit in a single byte (code points ≤ 0xFF) is stored one
// byte per character (the "compressed name" form), which the reader recovers
// directly and which every name in a Windows hive uses. A name with a wider rune
// falls back to UTF-16LE. The second return value is true when the compressed
// form was used, so the caller sets the matching flag.
func encodeName(name string) (b []byte, compressed bool) {
	if isLatin1(name) {
		out := make([]byte, 0, len(name))
		for _, r := range name {
			out = append(out, byte(r))
		}
		return out, true
	}
	u16 := utf16.Encode([]rune(name))
	out := make([]byte, 2*len(u16))
	for i, c := range u16 {
		out[2*i] = byte(c)
		out[2*i+1] = byte(c >> 8)
	}
	return out, false
}

// isLatin1 reports whether every rune of s fits in a single byte (≤ 0xFF), so the
// name can use the compressed one-byte-per-character form.
func isLatin1(s string) bool {
	for _, r := range s {
		if r > 0xFF {
			return false
		}
	}
	return true
}

// upperASCII upper-cases the ASCII letters of s and leaves other bytes untouched.
// It backs both the subkey ordering and the leaf-index hash, so the two agree and
// lookups resolve. Registry key and value names are ASCII in the hives handled
// here; a name with non-ASCII letters would be folded differently from the
// kernel's full-Unicode collation and could fail lookup — add a Unicode upcase
// table if such names ever appear.
func upperASCII(s string) string {
	var changed bool
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

// sortedChildren returns the subkeys ordered case-insensitively, the order the
// kernel binary-searches a leaf-index in. The upper-cased sort key is computed
// once per child (decorate-sort) rather than on every comparison.
func sortedChildren(subkeys []*Key) []*Key {
	type keyed struct {
		up  string
		key *Key
	}
	ks := make([]keyed, len(subkeys))
	for i, k := range subkeys {
		ks[i] = keyed{up: upperASCII(k.Name), key: k}
	}
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].up < ks[j].up })
	out := make([]*Key, len(ks))
	for i := range ks {
		out[i] = ks[i].key
	}
	return out
}

// align8 rounds n up to a multiple of the cell alignment.
func align8(n int) int { return roundUp(n, cellAlign) }

// roundUp rounds n up to a multiple of m.
func roundUp(n, m int) int { return (n + m - 1) / m * m }
