// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import "strings"

// expandStrings replaces %TOKEN% references in s with their [Strings] values.
// Tokens are matched case-insensitively; %% is a literal percent; an unknown token
// is left verbatim (including its percent signs) so the original text is
// recoverable and the problem is visible rather than silently dropped.
//
// PE Builder's own @VAR@ environment variables are not touched here — they are
// runtime substitutions applied at build time, not part of INF parsing.
func expandStrings(s string, table map[string]string) string {
	if !strings.ContainsRune(s, '%') || len(table) == 0 && !strings.Contains(s, "%%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// %% -> literal %
		if i+1 < len(s) && s[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		end := strings.IndexByte(s[i+1:], '%')
		if end < 0 {
			// No closing percent: emit the rest verbatim.
			b.WriteString(s[i:])
			break
		}
		token := s[i+1 : i+1+end]
		if val, ok := table[strings.ToLower(token)]; ok {
			b.WriteString(val)
		} else {
			// Unknown token: keep it literal, percents included.
			b.WriteString(s[i : i+1+end+1])
		}
		i += end + 2
	}
	return b.String()
}
