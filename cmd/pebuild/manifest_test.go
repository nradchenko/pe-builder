// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/peresource"
)

// The loader gives three resource ids a meaning, and naming them is why the listing is
// worth reading: a manifest at id 1 decides how the process is activated, one at any
// other id is read only by whoever asks for it by number.
func TestManifestIDName(t *testing.T) {
	for _, tc := range []struct {
		entry peresource.Entry
		want  string
	}{
		{peresource.Entry{ID: 1}, "CREATEPROCESS_MANIFEST_RESOURCE_ID"},
		{peresource.Entry{ID: 2}, "ISOLATIONAWARE_MANIFEST_RESOURCE_ID"},
		{peresource.Entry{ID: 3}, "ISOLATIONAWARE_NOSTATICIMPORT_MANIFEST_RESOURCE_ID"},
		{peresource.Entry{ID: 123}, ""}, // the id Windows' own binaries use, which the loader does not
		{peresource.Entry{ID: 0}, ""},
		// A named resource has no number, so none of the names above can apply to it.
		{peresource.Entry{Name: "SIDEBYSIDE"}, ""},
	} {
		got := manifestIDName(tc.entry)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%v named %q, want nothing", tc.entry, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%v named %q, want it to contain %q", tc.entry, got, tc.want)
		}
	}
}

// Bytes that are not the XML the caller came for are quoted rather than written
// through — a resource may hold anything, and a terminal is a poor place to find that
// out. The one-byte stub is the case this exists for.
func TestQuoteBytes(t *testing.T) {
	if got := quoteBytes([]byte(" ")); got != `" "` {
		t.Errorf("quoteBytes(space) = %s, want %q", got, " ")
	}
	if got := quoteBytes(nil); got != "(empty)" {
		t.Errorf("quoteBytes(nil) = %q, want %q", got, "(empty)")
	}
	// Unprintable bytes must not reach the terminal as themselves. Checked byte by
	// byte rather than by searching for them: a search string holding 0xff is not
	// valid UTF-8, and the search would quietly answer a different question.
	got := quoteBytes([]byte{0x00, 0x1b, 0xff})
	for i := 0; i < len(got); i++ {
		if got[i] < 0x20 || got[i] > 0x7e {
			t.Fatalf("quoteBytes passed byte %#x through at %d: %q", got[i], i, got)
		}
	}
	// A long resource is capped, and says so rather than appearing to be all there is.
	long := bytes.Repeat([]byte("A"), quoteBytesLimit+50)
	got = quoteBytes(long)
	if !strings.Contains(got, "in all") {
		t.Errorf("quoteBytes(long) = %q, want it to report the full length", got)
	}
	if len(got) > quoteBytesLimit+60 {
		t.Errorf("quoteBytes(long) returned %d bytes, want it capped near %d", len(got), quoteBytesLimit)
	}
}

// A manifest that declares an encoding is still a manifest. encoding/xml refuses any
// non-UTF-8 declaration outright when it has no CharsetReader, so without one this
// reported a perfectly good manifest as malformed for saying what it was encoded in.
func TestXMLProblemAcceptsADeclaredEncoding(t *testing.T) {
	for _, decl := range []string{"UTF-8", "utf-8", "windows-1252", "ISO-8859-1"} {
		doc := []byte(`<?xml version="1.0" encoding="` + decl + `"?><assembly/>`)
		if err := xmlProblem(doc); err != nil {
			t.Errorf("encoding %q reported as malformed: %v", decl, err)
		}
	}
}

// The stub at id 1 — the case the whole command exists to make visible.
func TestXMLProblemRejectsAStub(t *testing.T) {
	if err := xmlProblem([]byte(" ")); err == nil {
		t.Error("a one-byte stub was accepted as XML")
	}
	if err := xmlProblem(nil); err == nil {
		t.Error("an empty resource was accepted as XML")
	}
}

// Padding after the root element is reported rather than passed over. xml.Unmarshal
// stops at the end of the root and never sees it, which is how a resource carrying
// trailing NULs reads as well-formed.
func TestXMLProblemReadsPastTheRootElement(t *testing.T) {
	padded := append([]byte(`<assembly/>`), 0, 0, 0)
	if err := xmlProblem(padded); err == nil {
		t.Error("NUL padding after the root element was accepted")
	}
}

// What may be written to a terminal is decided by the bytes, independently of the
// parser's verdict — the guarantee is that binary never reaches a terminal, and it
// should not rest on a parser reaching the end of the buffer.
func TestPrintableText(t *testing.T) {
	for _, ok := range [][]byte{
		[]byte("<assembly/>"),
		[]byte("<assembly>\r\n\t<x/>\n</assembly>\n"),
		[]byte("<description>Ярлык</description>"), // non-ASCII text is still text
	} {
		if !printableText(ok) {
			t.Errorf("ordinary text was judged unprintable: %q", ok)
		}
	}
	for _, bad := range [][]byte{
		{0x00},
		{0x1b, '['},        // an escape sequence, which a terminal would act on
		{0xff, 0xfe, 0x00}, // a UTF-16 byte-order mark: not valid UTF-8
	} {
		if printableText(bad) {
			t.Errorf("unprintable bytes were judged printable: %q", bad)
		}
	}
}

// The stub at id 1 is a single space. Deciding on printability alone would write it
// through as a blank line — which reads as a manifest that is simply empty, rather than
// as the byte that makes the loader ignore the file staged beside the binary.
func TestStubIsShownAsItsBytes(t *testing.T) {
	stub := []byte(" ")
	if !printableText(stub) {
		t.Fatal("precondition: a space is printable, which is what makes this case a trap")
	}
	if xmlProblem(stub) == nil {
		t.Fatal("precondition: the stub is not well-formed XML")
	}
	if got := quoteBytes(stub); got != `" "` {
		t.Errorf("quoteBytes = %s, want the byte shown", got)
	}
}

// A file that is not a PE has to be reported against the name the caller asked for.
// Without the name, a command given several binaries says only that something was not
// a PE.
func TestPrintManifestsNamesTheFileItCouldNotRead(t *testing.T) {
	var buf bytes.Buffer
	err := printManifests(&buf, "notabinary.dll", "I386/NOTABINARY.DL_", []byte("plain text"))
	if err == nil {
		t.Fatal("a file that is not a PE was accepted")
	}
	if !strings.Contains(err.Error(), "notabinary.dll") {
		t.Errorf("error %q should name the file", err)
	}
}

// plural is used for the byte counts in the listing, where "1 bytes" would be the
// giveaway that nobody read the output.
func TestPlural(t *testing.T) {
	if got := plural(1, "byte"); got != "1 byte" {
		t.Errorf("plural(1) = %q, want %q", got, "1 byte")
	}
	for _, n := range []int{0, 2, 730} {
		want := "s"
		if got := plural(n, "byte"); !strings.HasSuffix(got, want) {
			t.Errorf("plural(%d) = %q, want it to end in %q", n, got, want)
		}
	}
}
