// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nradchenko/pe-builder/build"
	"github.com/nradchenko/pe-builder/receipt"
)

func testReceipt() *receipt.Receipt {
	return &receipt.Receipt{
		Schema:   receipt.Schema,
		BuiltAt:  time.Date(2026, 7, 21, 9, 14, 3, 0, time.UTC),
		PEBuild:  receipt.PEBuild{Version: "v0.5.0", EngineHash: "sha256:aa"},
		Manifest: receipt.Manifest{File: "luna.hcl", Digest: "sha256:bb"},
		Source:   receipt.Source{Identity: "winxp build 2600 (i386)", File: "winxp.iso"},
		Output:   receipt.Output{Target: "iso", VolumeLabel: "PEBUILDER"},
	}
}

// An image with no receipt gets an answer of its own. Calling it a mismatch would assert
// that it was built by a different pebuild, which is precisely what is not known.
func TestReadReceiptAbsentIsNotAMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "win51"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readReceipt(dir)
	if err == nil {
		t.Fatal("a tree with no receipt read without error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no build receipt") {
		t.Errorf("error = %q, want it to say the receipt is absent", msg)
	}
	// Check the wording, not the image path the message quotes back — a temp directory
	// carries the test's own name, which would match anything looked for here.
	if wording := strings.ReplaceAll(msg, dir, ""); strings.Contains(strings.ToLower(wording), "mismatch") {
		t.Errorf("an absent receipt was reported as a mismatch: %q", msg)
	}
}

// Show mode prints what the image says; it is the half that stays useful away from the
// tree that built the image, so it must not need anything but the receipt.
func TestShowReceiptPrintsPlainLines(t *testing.T) {
	var buf bytes.Buffer
	if err := showReceipt(&buf, testReceipt()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"file": "luna.hcl"`) {
		t.Errorf("output does not carry the receipt:\n%s", out)
	}
	// Stored CRLF for the PE; a terminal here gets plain lines.
	if strings.Contains(out, "\r") {
		t.Error("show mode printed CR characters to the terminal")
	}
}

// Show mode stands in for having *no* manifest, and nothing else. A pebuild.hcl that is
// present but invalid must be an error: falling back to printing the receipt would exit 0
// having verified nothing, so a CI gate (`pebuild verify img && deploy`) would pass on the
// strength of a manifest that never loaded — the exact false confidence this command
// exists to remove.
func TestVerifyRejectsAnInvalidManifestInsteadOfShowingTheReceipt(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := testReceipt().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, receipt.FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// A pebuild.hcl that resolves but does not parse.
	if err := os.WriteFile(filepath.Join(dir, "pebuild.hcl"), []byte("source { path = }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"verify", tree})

	if err := root.Execute(); err == nil {
		t.Errorf("an invalid pebuild.hcl was silently treated as no manifest; output:\n%s", out.String())
	}
	if strings.Contains(out.String(), `"schema"`) {
		t.Errorf("the receipt was printed instead of reporting the broken manifest:\n%s", out.String())
	}
}

func TestPrintVerification(t *testing.T) {
	cases := []struct {
		name    string
		fields  []build.Field
		want    string
		want2   string
		notWant string
	}{{
		name: "match",
		fields: []build.Field{
			{Name: "engine content", Value: "sha256:aa", Status: build.OK},
			{Name: "manifest", Value: "sha256:bb", Status: build.OK},
		},
		want: "MATCH:",
	}, {
		// A digest that differs has no per-item detail of its own, so the recorded value
		// has to be printed: "not the current one" cannot be acted on, while the digest
		// the image records is what identifies the build that did produce it.
		name: "a real difference names what the image recorded",
		fields: []build.Field{
			{Name: "engine content", Value: "sha256:cc", Recorded: "sha256:aa", Status: build.Changed},
		},
		want:  "built with sha256:aa",
		want2: "MISMATCH:",
	}, {
		// Nothing differed — something simply could not be checked. Saying "MISMATCH"
		// here would accuse the image of a change that was never observed.
		name: "only unknowns",
		fields: []build.Field{
			{Name: "engine content", Value: "sha256:aa", Status: build.OK},
			{Name: "source", Value: "winxp build 2600 (i386)", Status: build.Unknown,
				Detail: []string{"the manifest's source could not be read"}},
		},
		want:    "INCOMPLETE:",
		notWant: "MISMATCH:",
	}, {
		// An informational field never decides the verdict: a dirty version string with
		// identical engine content is the same build.
		name: "informational difference alone still matches",
		fields: []build.Field{
			{Name: "pebuild version", Value: "v0.5.0-dirty", Recorded: "v0.5.0", Informational: true},
			{Name: "engine content", Value: "sha256:aa", Status: build.OK},
		},
		want:    "MATCH:",
		notWant: "MISMATCH:",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printVerification(&buf, &build.Verification{Fields: tc.fields, Recorded: testReceipt()}, "luna.hcl")
			out := buf.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("output does not contain %q:\n%s", tc.want, out)
			}
			if tc.want2 != "" && !strings.Contains(out, tc.want2) {
				t.Errorf("output does not contain %q:\n%s", tc.want2, out)
			}
			if tc.notWant != "" && strings.Contains(out, tc.notWant) {
				t.Errorf("output wrongly contains %q:\n%s", tc.notWant, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if strings.HasSuffix(line, " ") {
					t.Errorf("line has trailing whitespace: %q", line)
				}
			}
		})
	}
}

// The taint verdict travels with the image, so it is reported whether or not the image
// matches — a provenance question wants it first.
func TestPrintVerificationShowsTaint(t *testing.T) {
	rec := testReceipt()
	rec.Tainted = true
	rec.Taint = []string{"out-of-tree:acme", "manifest-inject"}

	var buf bytes.Buffer
	printVerification(&buf, &build.Verification{
		Fields:   []build.Field{{Name: "engine content", Value: "sha256:aa", Status: build.OK}},
		Recorded: rec,
	}, "luna.hcl")

	out := buf.String()
	if !strings.Contains(out, "Tainted: out-of-tree:acme manifest-inject") {
		t.Errorf("a tainted build did not report its taint:\n%s", out)
	}
	if !strings.Contains(out, "MATCH:") {
		t.Errorf("taint changed the match verdict, which it must not:\n%s", out)
	}
}
