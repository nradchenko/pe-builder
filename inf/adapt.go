// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"strings"
	"unicode/utf16"
)

// AdaptNetworkComponent rewrites a network-component INF so its install can run in a PE.
//
// Scope, measured rather than assumed: of the INFs a build stages or might stage — the NIC
// INFs, machine/usb/input/keyboard/msmouse/mshdc, and the other network components
// (netnb, netmscli, netserv) — **only nettcpip.inf carries any of this**. TCP/IP is unusual in
// installing win32 co-services that come with security descriptors; an adapter INF installs a
// driver and has none. So this is not a general "adapt any INF" pass and must not be used as
// one: it is opt-in per file, and the caller is asserting the file is a network component.
//
// A component install (INetCfgClassSetup::Install) processes the component's own INF, and two
// things in a stock one cannot work in the environment we build:
//
//   - Security descriptors, in both the forms an INF states them: a Security = <SDDL> directive
//     in a service-install section, and a [<section>.Security] section, whose whole body is the
//     descriptor to apply to the keys its AddReg sibling creates. The image has empty SAM and
//     SECURITY hives, and Access Control is one of the Win32 API categories a PE does not carry
//     — so there is nothing to apply a descriptor with.
//   - ExcludeSetupStartServices, which in a stock INF names one service. The install would
//     otherwise start the component's co-services as part of installing it, and several are
//     svchost/lsass win32 services a minimal PE does not run. Widened to the component's whole
//     CoServices list, so the install registers them and starts none; bringing them up is then
//     a deliberate later step, in dependency order, by whoever wanted them.
//
// What goes is descriptors and nothing else. A `.Secure` section is not one of them, however it
// reads: in nettcpip.inf that suffix names an ordinary AddReg section that creates keys, and the
// descriptor for those keys lives in the `.Security` companion beside it — `.Security` is
// setupapi's construct, `.Secure` is the file's own naming. Dropping the pair together also
// dropped the key creation, and Tcpip\Parameters\PersistentRoutes is created nowhere else, so a
// persistent route had no key to be written into. Microsoft's PE-adapted copy drops both
// sections and puts that one key back by hand in the surviving sibling; keeping the
// key-creating section reaches the same place by a rule instead of a list, and leaves the three
// other keys of that section — which their copy expects something to create at run time —
// created outright.
//
// Removing descriptors is what Microsoft's own PE-adapted copy of nettcpip.inf does, arrived at
// by diffing it against the stock one; so is the ExcludeSetupStartServices widening, value for
// value. This transforms the *user's* INF rather than shipping Microsoft's, which is what keeps
// it clean-room: the rules are ours, the content stays theirs.
//
// The input may be UTF-16 (most INFs on XP media are) or plain bytes; the output keeps whatever
// the input used, because setupapi reads both and the file is staged as-is. An INF carrying
// none of these constructs comes back byte-identical, and so does every line of one that does.
//
// The invariant it holds whatever it is pointed at: a reference never outlives the section it
// names. A section this removes is also removed from the section-list directives that named it,
// and a directive left naming nothing goes with it, so the staged file cannot fail its install
// on a section that is not there. That is decided by which sections were actually dropped, not
// by how a name reads — Needs and Include may name a section in another file, and one of those
// is not this transform's to rewrite.
func AdaptNetworkComponent(data []byte) []byte {
	text, enc := decodeText(data)
	out := adaptText(text)
	return encodeText(out, enc)
}

var (
	// A section header, and whether it is a descriptor section that goes entirely.
	reSectionHdr      = regexp.MustCompile(`^\s*\[([^\]]*)\]\s*$`)
	reSecuritySection = regexp.MustCompile(`(?i)\.security$`)
	// [Strings], and its localized [Strings.0419] form. A token may be named anything, so a
	// definition there is not a directive and must not be read as one.
	reStringsSection = regexp.MustCompile(`(?i)^strings(\.|$)`)
	// A `Security = ...` directive on its own line.
	reSecurityLine = regexp.MustCompile(`(?i)^\s*Security\s*=`)
	// The directives whose value is a comma-separated list of section names. AddReg is the only
	// one nettcpip.inf points at a dropped section, but a reference surviving its section is
	// the one failure this must not produce, so every form that can hold one is scrubbed.
	reSectionList = regexp.MustCompile(`(?i)^(\s*(?:AddReg|DelReg|BitReg|Ini2Reg|Needs|Include)\s*=)(.*)$`)
	// The two Ndi values this cares about. The value is everything after the flags field.
	reCoServices = regexp.MustCompile(`(?i)^(\s*HKR\s*,\s*Ndi\s*,\s*CoServices\s*,\s*[^,]*,)(.*)$`)
	reExclude    = regexp.MustCompile(`(?i)^(\s*HKR\s*,\s*Ndi\s*,\s*ExcludeSetupStartServices\s*,\s*[^,]*,)(.*)$`)
)

// adaptText applies the rewrite to already-decoded text, preserving line endings.
//
// It walks logical lines — a directive may be continued across several physical ones — but
// emits physical ones, so a line it does not change is passed through exactly as it arrived.
// Only a line it rewrites is collapsed onto one, and only a whole logical line is ever dropped:
// dropping the first physical line of a continued directive would leave the rest of it behind
// as an orphan, and dropping a section reference without its continuation marker would leave
// the marker itself standing where a section name is expected.
func adaptText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	dropped := droppedSections(lines)

	// The CoServices value seen in the section being read. An INF declares CoServices before
	// the matching ExcludeSetupStartServices — they are adjacent in the same Ndi block — so
	// remembering it within the section pairs them without modelling scope properly.
	//
	// Reset at every section header, and that reset is the point: an INF with more than one
	// component would otherwise let one component's service list reach the next component's
	// ExcludeSetupStartServices, excluding the wrong services from starting. nettcpip.inf has
	// a single pair, so this cannot happen there — but the file this is pointed at is a
	// caller's choice, and a wrong exclusion list would be silent.
	coServices := ""
	dropping := false
	inStrings := false

	for i := 0; i < len(lines); {
		span, body := logicalLineAt(lines, i)
		group := lines[i : i+span]
		i += span

		// A section header always ends any drop in progress, and may start a new one.
		if m := reSectionHdr.FindStringSubmatch(strings.TrimRight(body, " \t")); m != nil {
			coServices = ""
			inStrings = reStringsSection.MatchString(strings.TrimSpace(m[1]))
			dropping = reSecuritySection.MatchString(strings.TrimSpace(m[1]))
			if dropping {
				continue
			}
			out = append(out, group...)
			continue
		}
		if dropping {
			continue
		}

		// A rewritten line collapses onto the last physical line's ending, and keeps that
		// line's own trailing comment — a note a reader wrote about this directive.
		last := group[len(group)-1]
		lastBody, eol := splitEOL(last)
		text, _ := stripComment(lastBody, false)
		comment := lastBody[len(text):]
		if comment != "" {
			// The run of spaces before the ';' belongs to the comment, not to the value being
			// replaced — carried across, or a preserved comment ends up jammed against it.
			comment = text[len(strings.TrimRight(text, " \t")):] + comment
		}

		// A [Strings] token may be named anything, Security included, and a definition there
		// is not the directive this drops. Losing it would leave every %Security% reference
		// in the file unresolvable, which fails the install for the sake of a name collision.
		if !inStrings && reSecurityLine.MatchString(body) {
			continue
		}
		if m := reCoServices.FindStringSubmatch(body); m != nil {
			coServices = strings.TrimSpace(m[2])
		}
		if m := reExclude.FindStringSubmatch(body); m != nil && coServices != "" {
			// One space after the flags comma, matching the file's own style, rather than
			// whatever spacing the CoServices line happened to use. INF value parsing
			// ignores the difference, but a staged file that differs from the reference
			// only in invisible whitespace is harder to verify than one that does not.
			out = append(out, m[1]+" "+coServices+comment+eol)
			continue
		}
		if m := reSectionList.FindStringSubmatch(body); m != nil {
			kept, removed := keepNonSecurity(m[2], dropped)
			if removed == 0 {
				// Nothing to remove. Emit what arrived rather than a reconstruction of it,
				// or every INF with a section list comes back reformatted — different
				// spacing, no trailing comments — for no change in what it installs.
				out = append(out, group...)
				continue
			}
			if len(kept) == 0 {
				// Every section it named was a descriptor section; the directive has nothing
				// left to do, and an empty AddReg= is not valid.
				continue
			}
			out = append(out, m[1]+" "+strings.Join(kept, ", ")+comment+eol)
			continue
		}
		out = append(out, group...)
	}
	return strings.Join(out, "\n")
}

// logicalLineAt reports how many physical lines starting at i make up one logical line, and
// that line's joined, comment-free text.
//
// It applies the same rules as the lexer's logicalLines — comment stripped per physical line
// before the continuation test, quote state threaded across the join — but over a slice, so the
// caller still holds the physical lines and can emit them untouched. Sharing the rules is the
// point: a directive the parser reads as one line has to be one line here too, or this decides
// the fate of half of it.
func logicalLineAt(lines []string, i int) (span int, body string) {
	var b strings.Builder
	inQuote := false

	for j := i; j < len(lines); j++ {
		text, endInQuote := stripComment(strings.TrimRight(lines[j], "\r"), inQuote)
		trimmed := strings.TrimRight(text, " \t")
		if strings.HasSuffix(trimmed, `\`) && j+1 < len(lines) {
			b.WriteString(strings.TrimSuffix(trimmed, `\`))
			inQuote = endInQuote
			continue
		}
		b.WriteString(text)
		return j - i + 1, b.String()
	}
	// A trailing continuation with no line after it, as the lexer treats it: what we have.
	return len(lines) - i, b.String()
}

// droppedSections collects the sections this file loses, so a reference can be tested against
// what was actually removed rather than against the name it has.
//
// The difference matters for a directive that names a section in another file — Needs and
// Include do — where a `.Security` suffix says nothing about whether the section is still
// there. Matching by name would delete a reference to a section nobody dropped. It also has to
// be a pass of its own: a section list may name a section defined further down the file, so no
// single pass could know yet.
func droppedSections(lines []string) map[string]bool {
	dropped := map[string]bool{}

	for i := 0; i < len(lines); {
		span, body := logicalLineAt(lines, i)
		i += span
		if m := reSectionHdr.FindStringSubmatch(strings.TrimRight(body, " \t")); m != nil {
			if name := strings.TrimSpace(m[1]); reSecuritySection.MatchString(name) {
				dropped[strings.ToLower(name)] = true
			}
		}
	}
	return dropped
}

// keepNonSecurity splits a section-list value and removes the names of sections this file no
// longer defines, reporting how many it removed so a caller can tell "nothing to do" from
// "emptied".
//
// The list arrives already comment-free and continuation-joined, which is what makes the name
// test reliable: `AddReg = A, B.Security ; note` would otherwise leave the last name as
// "B.Security ; note", matching nothing — so the reference would survive while the section it
// names had been dropped, and setupapi would fail the install on a section that is not there.
// A reference outliving its section is the one failure this exists to prevent.
func keepNonSecurity(list string, dropped map[string]bool) (kept []string, removed int) {
	for _, part := range strings.Split(list, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if dropped[strings.ToLower(name)] {
			removed++
			continue
		}
		kept = append(kept, name)
	}
	return kept, removed
}

// splitEOL separates a trailing carriage return so a rewritten line keeps the file's endings.
func splitEOL(line string) (body, eol string) {
	if strings.HasSuffix(line, "\r") {
		return line[:len(line)-1], "\r"
	}
	return line, ""
}

// infEncoding records how an INF's bytes were encoded, so a rewritten one can be written back
// the same way. A staged INF is read by setupapi, not by us, and it reads what it is given —
// so the transform must not quietly change the file's encoding along with its content.
type infEncoding int

const (
	encPlain infEncoding = iota // no BOM: single-byte text of some code page
	encUTF16LE
	encUTF16BE
	encUTF8BOM
)

// decodeText reads an INF's bytes as text and reports the encoding, reusing the BOM detection
// the parser already applies rather than repeating the rules.
func decodeText(data []byte) (string, infEncoding) {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return string(Decode(data)), encUTF16LE
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return string(Decode(data)), encUTF16BE
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return string(Decode(data)), encUTF8BOM
	default:
		return string(data), encPlain
	}
}

// encodeText is decodeText's inverse.
func encodeText(text string, enc infEncoding) []byte {
	switch enc {
	case encUTF16LE, encUTF16BE:
		order := binary.ByteOrder(binary.LittleEndian)
		bom := []byte{0xFF, 0xFE}
		if enc == encUTF16BE {
			order, bom = binary.BigEndian, []byte{0xFE, 0xFF}
		}
		u16 := utf16.Encode([]rune(text))
		buf := bytes.NewBuffer(make([]byte, 0, 2+len(u16)*2))
		buf.Write(bom)
		for _, u := range u16 {
			var b [2]byte
			order.PutUint16(b[:], u)
			buf.Write(b[:])
		}
		return buf.Bytes()
	case encUTF8BOM:
		return append([]byte{0xEF, 0xBB, 0xBF}, text...)
	default:
		return []byte(text)
	}
}
