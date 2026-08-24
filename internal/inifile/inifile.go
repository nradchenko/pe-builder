// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package inifile applies the base layer's and plugins' text-edit actions to the
// INI/INF files already placed in the output tree — most importantly txtsetup.sif,
// whose osloadoptions edit switches the loader into PE mode.
//
// The edits are deliberately surgical. These are Microsoft's own setup files, so
// the editor changes only the targeted value or line and leaves the rest of the
// file — its ordering, comments, blank lines, and CRLF line endings — intact. A
// value that already exists is replaced in place; a new one is appended to its
// section. Windows setup INFs may repeat a section name (setupapi merges the
// blocks), so a value is looked up across every block of its section.
package inifile

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/internal/action"
)

// Apply executes the text-edit actions (SetValue / AddLine / DelLine) against the
// files they target, each edited wherever the file-copy stage actually placed it.
//
// placed maps a target's lowercased base name to its tree-relative (forward-slash)
// path — build it with Index from the copy stage's written-file list. A target no
// copy placed is skipped with a warning (its edit belongs to a file this build did
// not include, e.g. a plugin INF another plugin would have supplied); a target that
// was placed but cannot be read or written back is a hard error, so the base's
// must-land edits (the /minint switch) cannot be silently lost.
func Apply(acts []action.Action, treeRoot string, placed map[string]string) error {
	// Preserve action order per file.
	var order []string
	byFile := map[string][]action.Action{}
	for _, a := range acts {
		switch a.Kind {
		case action.SetValue, action.AddLine, action.DelLine:
			if _, seen := byFile[a.TargetFile]; !seen {
				order = append(order, a.TargetFile)
			}
			byFile[a.TargetFile] = append(byFile[a.TargetFile], a)
		}
	}

	for _, target := range order {
		rel, ok := placed[baseName(target)]
		if !ok {
			slog.Warn("skipping text edits for a file not in the output tree", "file", target)
			continue
		}
		file := filepath.Join(treeRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("inifile: read %s: %w", target, err)
		}

		// Split (and later rejoin) once per file, not once per edit.
		lines := strings.Split(string(data), "\n")
		eol := fileEOL(string(data))
		for _, a := range byFile[target] {
			switch a.Kind {
			case action.SetValue:
				lines = setValue(lines, eol, a.TargetSection, a.LineKey, a.LineData)
			case action.AddLine:
				lines = addLine(lines, eol, a.TargetSection, a.LineData)
			case action.DelLine:
				lines = delLine(lines, a.TargetSection, a.LineData)
			}
		}
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return fmt.Errorf("inifile: write %s: %w", target, err)
		}
	}
	return nil
}

// Index builds the base-name → tree-relative-path map Apply consumes, from the
// output-relative paths the file-copy stage wrote. A later path wins, matching the
// copy stage's last-writer-wins placement.
func Index(written []string) map[string]string {
	m := make(map[string]string, len(written))
	for _, p := range written {
		m[baseName(p)] = p
	}
	return m
}

// baseName is the lowercased final path component, tolerating both slash styles so
// a target written "inf\foo.inf" matches a file placed at "i386/inf/foo.inf".
func baseName(p string) string {
	return strings.ToLower(path.Base(strings.ReplaceAll(p, `\`, "/")))
}

// SetValue sets key = value inside [section] (all string-based helpers operate on
// whole file content; Apply uses the line-based forms to avoid re-splitting). If
// the key exists in any block of the section it is replaced in place; otherwise a
// "key = value" line is appended to the last block. A missing section is created
// at the end of the file.
func SetValue(content, section, key, value string) string {
	return join(setValue(strings.Split(content, "\n"), fileEOL(content), section, key, value))
}

// AddLine appends a raw directive line to [section] unless it is already present.
func AddLine(content, section, line string) string {
	return join(addLine(strings.Split(content, "\n"), fileEOL(content), section, line))
}

// DelLine removes every line in [section] whose trimmed text begins with prefix.
func DelLine(content, section, prefix string) string {
	return join(delLine(strings.Split(content, "\n"), section, prefix))
}

func join(lines []string) string { return strings.Join(lines, "\n") }

// setValue is the line-based form used by Apply and SetValue.
func setValue(lines []string, eol, section, key, value string) []string {
	ranges := sectionRanges(lines, section)
	if len(ranges) == 0 {
		return appendSection(lines, eol, section, key+" = "+value)
	}
	for _, r := range ranges {
		for i := r[0]; i < r[1]; i++ {
			if k, isKV := splitKey(lines[i]); isKV && strings.EqualFold(strings.TrimSpace(k), key) {
				lines[i] = setLineValue(lines[i], value)
				return lines
			}
		}
	}
	// Absent: insert after the last content line of the last block.
	last := ranges[len(ranges)-1]
	return insertAt(lines, lastContentLine(lines, last[0], last[1])+1, key+" = "+value+eol)
}

func addLine(lines []string, eol, section, line string) []string {
	ranges := sectionRanges(lines, section)
	if len(ranges) == 0 {
		return appendSection(lines, eol, section, line)
	}
	want := strings.TrimSpace(line)
	for _, r := range ranges {
		for i := r[0]; i < r[1]; i++ {
			if strings.TrimSpace(trimEOL(lines[i])) == want {
				return lines // already present
			}
		}
	}
	last := ranges[len(ranges)-1]
	return insertAt(lines, lastContentLine(lines, last[0], last[1])+1, line+eol)
}

func delLine(lines []string, section, prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return lines
	}
	drop := map[int]bool{}
	for _, r := range sectionRanges(lines, section) {
		for i := r[0]; i < r[1]; i++ {
			if strings.HasPrefix(strings.TrimSpace(trimEOL(lines[i])), prefix) {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for i, ln := range lines {
		if !drop[i] {
			out = append(out, ln)
		}
	}
	return out
}

// sectionRanges returns the [start,end) body ranges (excluding the header line) of
// every block of the named section, matched case-insensitively, in file order.
// end is the index of the next section header or len(lines).
func sectionRanges(lines []string, section string) [][2]int {
	var ranges [][2]int
	for i := 0; i < len(lines); i++ {
		name, isHdr := isSectionHeader(lines[i])
		if !isHdr || !strings.EqualFold(name, section) {
			continue
		}
		start, end := i+1, len(lines)
		for j := start; j < len(lines); j++ {
			if _, h := isSectionHeader(lines[j]); h {
				end = j
				break
			}
		}
		ranges = append(ranges, [2]int{start, end})
		i = end - 1
	}
	return ranges
}

// insertAt returns lines with line spliced in at index at.
func insertAt(lines []string, at int, line string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, line)
	return append(out, lines[at:]...)
}

// appendSection adds a new "[section]" with a single line, inserted before any
// trailing blank lines so the file's final blank padding (and terminating newline)
// is preserved rather than trimmed away.
func appendSection(lines []string, eol, section, line string) []string {
	at := len(lines)
	for at > 0 && strings.TrimSpace(trimEOL(lines[at-1])) == "" {
		at--
	}
	block := []string{"[" + section + "]" + eol, line + eol}
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:at]...)
	out = append(out, block...)
	return append(out, lines[at:]...)
}

// isSectionHeader reports whether a line is a "[name]" header, and the name.
func isSectionHeader(line string) (string, bool) {
	t := strings.TrimSpace(trimEOL(line))
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// splitKey returns the key (text left of the first '=') of a key/value line, and
// whether the line is one. A key never contains '=', so splitting on the first is
// correct; whatever follows '=' (values that may themselves contain '=') is left
// untouched.
func splitKey(line string) (key string, isKV bool) {
	body := trimEOL(line)
	if i := strings.IndexByte(body, '='); i >= 0 {
		return body[:i], true
	}
	return "", false
}

// setLineValue replaces the value (right of the first '=') of a key/value line,
// preserving the key text, the separator, and the line's own end-of-line marker.
func setLineValue(line, value string) string {
	eol := ""
	if strings.HasSuffix(line, "\r") {
		eol = "\r"
	}
	body := trimEOL(line)
	i := strings.IndexByte(body, '=')
	if i < 0 {
		return line
	}
	return body[:i+1] + " " + value + eol
}

// lastContentLine returns the index of the last non-blank line within [start,end),
// or start-1 when the body is empty, so an inserted line lands after the block's
// real content rather than after its trailing blank lines.
func lastContentLine(lines []string, start, end int) int {
	last := start - 1
	for i := start; i < end; i++ {
		if strings.TrimSpace(trimEOL(lines[i])) != "" {
			last = i
		}
	}
	return last
}

// trimEOL returns the line without its trailing carriage return.
func trimEOL(line string) string { return strings.TrimSuffix(line, "\r") }

// fileEOL is the end-of-line marker to give newly inserted lines: "\r" (paired
// with the "\n" from the join) for a CRLF file, "" for LF. Existing lines keep
// their own ending; only insertions and new sections use this.
func fileEOL(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r"
	}
	return ""
}
