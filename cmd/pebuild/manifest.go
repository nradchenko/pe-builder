// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/peresource"
	"github.com/nradchenko/pe-builder/source"
)

// manifestLong is shared by both commands, which differ only in what they read the
// binary out of.
const manifestLong = "Print the side-by-side manifests a Windows binary carries, with the resource\n" +
	"id each one sits at.\n\n" +
	"The id is the point. The Windows loader takes a process's manifest from the\n" +
	"resource numbered 1 and, having found one, ignores a .manifest file placed beside\n" +
	"the binary — so a binary whose real manifest is at another id behaves differently\n" +
	"from one that has none at all, and the two are indistinguishable without looking."

func newSourceManifestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manifest <source> <binary>...",
		Short: "Print the manifests a binary in a source carries",
		Long: manifestLong + "\n\nThe binary is named the way `source extract` names one: by bare name, resolved\n" +
			"against every form the media stores it in, or by its exact path when a bare name\n" +
			"is ambiguous. It is decompressed first, so a binary the media keeps in a cabinet\n" +
			"or as a compressed *.??_ archive reads the same as a loose one.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			idx, err := openSourceIndex(cmd.ErrOrStderr(), src)
			if err != nil {
				return err
			}
			return eachManifest(cmd.OutOrStdout(), args[1:], func(name string) ([]byte, string, error) {
				data, _, origin, err := extractFile(src.Backing(), idx, name, false)
				return data, origin, err
			})
		},
	}
}

func newImageManifestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manifest <image> <binary>...",
		Short: "Print the manifests a binary in a built image carries",
		Long: manifestLong + "\n\nThe binary is named the way `image extract` names one: by exact path, or by bare\n" +
			"name when that name is unique in the image.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := openImage(args[0])
			if err != nil {
				return err
			}
			paths, err := walkImage(b)
			if err != nil {
				return err
			}
			return eachManifest(cmd.OutOrStdout(), args[1:], func(name string) ([]byte, string, error) {
				from, err := resolveImageName(b, paths, name)
				if err != nil {
					return nil, "", err
				}
				data, err := fs.ReadFile(b, from)
				return data, from, err
			})
		},
	}
}

// eachManifest reads and prints every named binary. read resolves a name to the
// binary's bytes and to where they came from, which the two commands answer
// differently.
//
// A name that cannot be resolved or read stops the command rather than being reported
// and skipped: a caller comparing two binaries wants to know that one of them was
// never examined, not to read a report with a gap in it.
func eachManifest(w io.Writer, names []string, read func(string) ([]byte, string, error)) error {
	for i, name := range names {
		data, origin, err := read(name)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := printManifests(w, name, origin, data); err != nil {
			return err
		}
	}
	return nil
}

// printManifests writes every RT_MANIFEST resource in one binary.
func printManifests(w io.Writer, name, origin string, image []byte) error {
	entries, err := peresource.Read(image, peresource.TypeManifest)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	fmt.Fprintf(w, "%s (from %s)\n", name, origin)
	if len(entries) == 0 {
		fmt.Fprintln(w, "\n  no manifest resource")
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(w, "\n  %s%s, language %d, %s\n", e, manifestIDName(e), e.Lang, plural(len(e.Data), "byte"))
		// A manifest that is not well-formed XML is worth saying so about rather than
		// leaving the reader to judge it from a byte count: a stub at id 1 carries
		// nothing, yet is enough to make the loader ignore an external manifest.
		problem := xmlProblem(e.Data)
		if problem != nil {
			fmt.Fprintf(w, "  not well-formed XML: %v\n", problem)
		}
		// Written through only when it is both the XML the caller came for and
		// printable. Either test alone lets something through: the stub at id 1 is a
		// single space, which is printable and would render as a blank line rather
		// than as the nothing it is; and a clean parse is no guarantee about the
		// bytes, since a decoder that stopped at the root element would never have
		// looked at padding after it.
		if problem != nil || !printableText(e.Data) {
			fmt.Fprintf(w, "  contents: %s\n", quoteBytes(e.Data))
			continue
		}
		fmt.Fprintf(w, "%s\n", strings.TrimRight(string(e.Data), "\r\n"))
	}
	return nil
}

// manifestIDName names the resource ids the loader itself gives meaning to, so a
// listing says which of several manifests is the one that decides the binary's
// behaviour. It returns the empty string for any other id, and for a named resource.
func manifestIDName(e peresource.Entry) string {
	if e.Named() {
		return ""
	}
	switch e.ID {
	case peresource.CreateProcessManifest:
		return " (CREATEPROCESS_MANIFEST_RESOURCE_ID — the one the loader applies to a process)"
	case peresource.IsolationAwareManifest:
		return " (ISOLATIONAWARE_MANIFEST_RESOURCE_ID — the one a DLL is activated with)"
	case peresource.IsolationAwareNoStaticImportManifest:
		return " (ISOLATIONAWARE_NOSTATICIMPORT_MANIFEST_RESOURCE_ID)"
	}
	return ""
}

// xmlProblem reports why a resource is not well-formed XML, or nil.
//
// A declared encoding is accepted rather than refused. encoding/xml rejects any
// non-UTF-8 declaration outright when it has no CharsetReader, which would report a
// perfectly good manifest as malformed for saying so — and the question being asked
// here is whether the resource is a manifest at all, not which encoding it announced.
// It reads to the end rather than stopping at the root element, so padding or junk
// after the document is reported rather than passed over — and it requires an element
// to have been seen, since a resource holding nothing but whitespace reaches the end
// without complaint and is not a manifest.
func xmlProblem(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var sawElement bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !sawElement {
				return errors.New("no XML element")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok := tok.(xml.StartElement); ok {
			sawElement = true
		}
	}
}

// printableText reports whether bytes can be written to a terminal as themselves:
// valid UTF-8 carrying no control characters other than the whitespace that structures
// a text file.
func printableText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// quoteBytesLimit caps how much of a resource that is not XML is shown. Enough to
// recognise what is there; not enough to fill a terminal with a resource that turned
// out to be something else entirely.
const quoteBytesLimit = 200

// quoteBytes renders bytes safely for a terminal, escaping anything unprintable and
// noting what it left out.
func quoteBytes(b []byte) string {
	if len(b) == 0 {
		return "(empty)"
	}
	if len(b) <= quoteBytesLimit {
		return fmt.Sprintf("%q", b)
	}
	return fmt.Sprintf("%q… (%s in all)", b[:quoteBytesLimit], plural(len(b), "byte"))
}

// plural renders a count with its unit, pluralized by adding an s — which is correct
// for every unit this file uses.
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
