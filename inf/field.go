// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import (
	"strconv"
	"strings"
)

// splitFields splits an INF value into comma-separated fields following the
// setupapi rules: a field may be double-quoted, inside which "" is a literal quote
// and commas are not separators. Unquoted whitespace around a field is trimmed,
// but whitespace inside quotes is preserved exactly.
//
// A trailing unquoted ';' begins a comment and ends the value (comment stripping
// also happens at the line level, but handling it here keeps field parsing robust
// for values assembled from continuations).
func splitFields(s string) []string {
	var fields []string
	var b strings.Builder
	var pending []byte // unemitted whitespace after started content, dropped if trailing
	inQuote := false
	started := false // any non-whitespace content seen for the current field

	// commitPending writes buffered inter-word whitespace before the next content,
	// so interior spaces survive while trailing unquoted spaces (never committed)
	// are dropped at flush.
	commitPending := func() {
		b.Write(pending)
		pending = pending[:0]
	}
	flush := func() {
		fields = append(fields, b.String())
		b.Reset()
		pending = pending[:0]
		started = false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '"' {
				// A doubled quote inside a quoted field is a literal quote.
				if i+1 < len(s) && s[i+1] == '"' {
					b.WriteByte('"')
					i++
				} else {
					inQuote = false
				}
			} else {
				b.WriteByte(c)
			}
		case c == '"':
			// A quote toggles literal mode; any content already accumulated for this
			// field (and interior whitespace before the quote) is kept.
			commitPending()
			inQuote = true
			started = true
		case c == ',':
			flush()
		case c == ';':
			// Unquoted comment: ends the value.
			flush()
			return fields
		case c == ' ' || c == '\t':
			// Leading unquoted whitespace is skipped; interior whitespace is buffered
			// until the next content proves it non-trailing.
			if started {
				pending = append(pending, c)
			}
		default:
			commitPending()
			started = true
			b.WriteByte(c)
		}
	}
	flush()
	return fields
}

// ParseInt reads a setupapi integer field: a 0x-prefixed hex or decimal value
// with an optional sign. It is the shared parser for INF integer fields, exported
// so other packages honoring the same convention (e.g. registry value data) do
// not reimplement it.
func ParseInt(s string) (int64, bool) { return parseInt(s) }

// parseInt reads a setupapi integer field: 0x-prefixed hex or decimal, with an
// optional sign.
func parseInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}
	var (
		n   int64
		err error
	)
	if len(s) > 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		n, err = strconv.ParseInt(s[2:], 16, 64)
	} else {
		n, err = strconv.ParseInt(s, 10, 64)
	}
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

// parseHexByte reads a single hex byte field (e.g. "98", "0x20", "80").
func parseHexByte(s string) (byte, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(n), true
}
