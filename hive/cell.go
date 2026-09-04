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

// unicodeNameLen returns how many bytes an encoded name would occupy as UTF-16. The nk
// maxima count every name that way, including one stored in the compressed one-byte-per-
// character form, whose stored length is therefore half the number they want.
func unicodeNameLen(nameBytes []byte, compressed bool) uint32 {
	if compressed {
		return uint32(2 * len(nameBytes))
	}
	return uint32(len(nameBytes))
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
// It backs the subkey ordering, and that ordering is load-bearing: the kernel binary-searches
// a leaf, so a sibling the writer sorted into the wrong place cannot be found at all.
//
// Non-ASCII key names do occur — localized media declare a few under
// Software\Microsoft\Windows NT\CurrentVersion\Network: four CJK names on Simplified Chinese
// Server 2003, one Arabic on Arabic XP, one Cyrillic ("Просмотр событий") on Russian XP. The
// first two scripts are uncased, so this fold and the kernel's upcase agree on them, and for
// BMP characters UTF-8 byte order matches the UTF-16 code-unit order the kernel compares in.
// The Cyrillic one is cased and is folded differently here than the kernel folds it, but it is
// the only non-ASCII name among its siblings and every ASCII sibling sorts below U+0400 under
// either fold, so the order still agrees.
//
// What that leaves is a narrow gap rather than a live fault: two or more *cased* non-ASCII
// siblings under one key could be ordered differently from the kernel's collation, and the
// second one would be unfindable. Closing it needs the kernel's upcase table, not Go's
// unicode.ToUpper, which disagrees with it in places — measure before reaching for either.
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
