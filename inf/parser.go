// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package inf parses a pragmatic subset of the Windows setupapi INF grammar — the
// format PE Builder plugins and the base manifest are written in. It handles
// section headers, comments, backslash line continuation, [Strings] %TOKEN%
// substitution, comma-separated double-quoted fields, and .BuildNo conditional
// sections. See docs/reference/03-plugin-format.md for the format.
package inf

import (
	"fmt"
	"strconv"
	"strings"
)

// File is a parsed INF: its sections (in encounter order) and its [Strings] table.
type File struct {
	sections map[string][]*Line
	order    []string
	strings  map[string]string
}

// Line is one directive within a section: an optional key (left of '=') and the
// comma-separated value fields (the rest). Fields are unquoted and %TOKEN%-expanded
// on access.
type Line struct {
	// Key is the text left of the first top-level '=', or "" for a keyless line
	// (such as a registry directive). It is not %TOKEN%-expanded.
	Key string

	raw    string
	file   *File
	fields []string // memoized Fields() result; nil until first access
}

const stringsSection = "strings"

// Parse reads INF text into a File. The input is Decode'd first, so a UTF-16 file
// off a Windows source parses like any other; the decoded text is then treated as
// ASCII/CP-agnostic bytes, with only line structure and the ASCII metacharacters
// interpreted.
func Parse(data []byte) (*File, error) {
	f := &File{
		sections: map[string][]*Line{},
		strings:  map[string]string{},
	}

	current := "" // lowercased current section name; "" before the first header
	for _, raw := range logicalLines(string(Decode(data))) {
		// Comments are already stripped by logicalLines.
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if name, ok := sectionHeader(line); ok {
			current = strings.ToLower(name)
			if _, seen := f.sections[current]; !seen {
				f.sections[current] = nil
				f.order = append(f.order, current)
			}
			continue
		}
		if current == "" {
			// A directive before any section header is not valid INF.
			return nil, fmt.Errorf("directive %q appears before any section", line)
		}

		key, value := splitKeyValue(line)
		if current == stringsSection {
			// [Strings] entries populate the substitution table; a token with no key
			// is ignored.
			if key != "" {
				f.strings[strings.ToLower(key)] = firstField(value)
			}
			continue
		}
		f.sections[current] = append(f.sections[current], &Line{Key: key, raw: value, file: f})
	}
	return f, nil
}

// sectionHeader reports whether line is a "[name]" header and returns the name.
func sectionHeader(line string) (string, bool) {
	if len(line) < 2 || line[0] != '[' {
		return "", false
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(line[1:end]), true
}

// Section returns the lines for base section name, including any ".<build>"
// build-conditional variant, in file order (the unsuffixed section first, then the
// build-specific tail). A build of 0 selects only the unsuffixed section.
func (f *File) Section(base string, build int) []*Line {
	lines := append([]*Line(nil), f.sections[strings.ToLower(base)]...)
	if build != 0 {
		suffix := strings.ToLower(base) + "." + strconv.Itoa(build)
		lines = append(lines, f.sections[suffix]...)
	}
	return lines
}

// HasSection reports whether the base section (unsuffixed) is present.
func (f *File) HasSection(base string) bool {
	_, ok := f.sections[strings.ToLower(base)]
	return ok
}

// Sections returns the section names present, in encounter order (lowercased,
// excluding [Strings]).
func (f *File) Sections() []string {
	out := make([]string, 0, len(f.order))
	for _, s := range f.order {
		if s != stringsSection {
			out = append(out, s)
		}
	}
	return out
}

// Strings returns the value of a [Strings] token (case-insensitive), or "" and
// false if absent.
func (f *File) Strings(token string) (string, bool) {
	v, ok := f.strings[strings.ToLower(token)]
	return v, ok
}

// Fields returns the line's value split into comma-separated fields, each unquoted
// and %TOKEN%-expanded. The result is computed once and cached: a single line is
// read for several fields while compiling one action, and splitFields always
// returns at least one element (so a nil cache reliably means "not yet computed").
// Not safe for concurrent first access; INF parsing and evaluation are single-
// threaded.
func (l *Line) Fields() []string {
	if l.fields == nil {
		fields := splitFields(l.raw)
		for i, f := range fields {
			fields[i] = expandStrings(f, l.file.strings)
		}
		l.fields = fields
	}
	return l.fields
}

// Field returns the i-th value field (0-based), or "" if out of range.
func (l *Line) Field(i int) string {
	fields := l.Fields()
	if i < 0 || i >= len(fields) {
		return ""
	}
	return fields[i]
}

// IntField reads the i-th field as an integer (0x-hex or decimal).
func (l *Line) IntField(i int) (int64, bool) {
	return parseInt(l.Field(i))
}

// BinaryFields reads fields [from:] as a hex byte sequence (e.g. a REG_BINARY
// value written as comma-separated hex bytes). It fails if any field is not a byte.
func (l *Line) BinaryFields(from int) ([]byte, bool) {
	fields := l.Fields()
	if from < 0 || from > len(fields) {
		return nil, false
	}
	out := make([]byte, 0, len(fields)-from)
	for _, f := range fields[from:] {
		b, ok := parseHexByte(f)
		if !ok {
			return nil, false
		}
		out = append(out, b)
	}
	return out, true
}

// Raw returns the line's verbatim value text (right of '='), before field splitting.
func (l *Line) Raw() string { return l.raw }

// firstField returns the first unquoted field of s (used for [Strings] values,
// which are a single possibly-quoted string).
func firstField(s string) string {
	fields := splitFields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
