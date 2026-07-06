// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import "strings"

// logicalLines splits INF text into logical lines: physical lines (CRLF, CR, or LF
// terminated) with '\'-continuation applied — a line whose last non-whitespace
// character is a backslash is joined with the line that follows.
//
// Comments are stripped per physical line *before* the continuation test, so a
// continued line may carry a trailing comment (the '\' is found once the comment
// is gone) and a '\' inside a comment does not trigger a spurious join. The quote
// state is threaded across continued lines so a ';' inside a double-quoted value
// that spans a continuation is not mistaken for a comment.
func logicalLines(text string) []string {
	// Normalize line endings, then split.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	physical := strings.Split(text, "\n")

	var lines []string
	var pending strings.Builder
	continuing := false
	inQuote := false // carried across a '\' continuation; reset at each logical line
	for _, ln := range physical {
		ln, endInQuote := stripComment(ln, inQuote)
		trimmed := strings.TrimRight(ln, " \t")
		if strings.HasSuffix(trimmed, `\`) {
			pending.WriteString(strings.TrimSuffix(trimmed, `\`))
			continuing = true
			inQuote = endInQuote // a quote left open continues into the next line
			continue
		}
		if continuing {
			pending.WriteString(ln)
			lines = append(lines, pending.String())
			pending.Reset()
			continuing = false
		} else {
			lines = append(lines, ln)
		}
		inQuote = false
	}
	if continuing {
		// Trailing continuation with no following line: keep what we have.
		lines = append(lines, pending.String())
	}
	return lines
}

// stripComment removes a trailing ';' comment from a physical line, ignoring
// semicolons inside double-quoted spans (where "" is a literal quote). startInQuote
// is the quote state carried in from a previous continued line; the returned bool
// is the quote state at the end of the returned text, so the caller can thread it
// through a continuation.
func stripComment(s string, startInQuote bool) (string, bool) {
	inQuote := startInQuote
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			if inQuote && i+1 < len(s) && s[i+1] == '"' {
				i++ // doubled quote: stays inside the string
				continue
			}
			inQuote = !inQuote
		case ';':
			if !inQuote {
				return s[:i], false
			}
		}
	}
	return s, inQuote
}

// splitKeyValue splits a line into its key (left of the first top-level '=') and
// raw value (the rest). A line with no top-level '=' is keyless: key is empty and
// the whole line is the value. The '=' search ignores quoted spans.
func splitKeyValue(s string) (key, raw string) {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			if inQuote && i+1 < len(s) && s[i+1] == '"' {
				i++
				continue
			}
			inQuote = !inQuote
		case '=':
			if !inQuote {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
			}
		}
	}
	return "", strings.TrimSpace(s)
}
