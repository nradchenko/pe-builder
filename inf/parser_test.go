// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/golden"
)

// render dumps a parsed File to a stable textual form for golden comparison.
func render(f *File) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# sections")
	for _, name := range f.Sections() {
		fmt.Fprintf(&b, "[%s]\n", name)
		for _, l := range f.sections[name] {
			fmt.Fprintf(&b, "  key=%q fields=%q\n", l.Key, l.Fields())
		}
	}
	fmt.Fprintln(&b, "# Files @ build 2600 (base + .2600)")
	for _, l := range f.Section("Files", 2600) {
		fmt.Fprintf(&b, "  key=%q fields=%q\n", l.Key, l.Fields())
	}
	return b.String()
}

func TestParseSampleGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.inf")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "sample", render(f))
}

func TestStringsSubstitution(t *testing.T) {
	f := mustParse(t, `[Notes]
a=%Product%
b=%Undefined%
c=100%% done

[Strings]
Product="Sample Widget"`)
	lines := f.Section("Notes", 0)
	got := []string{lines[0].Field(0), lines[1].Field(0), lines[2].Field(0)}
	want := []string{"Sample Widget", "%Undefined%", "100% done"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestContinuation(t *testing.T) {
	f := mustParse(t, "[S]\nx = a, \\\n    b, \\\n    c")
	got := f.Section("S", 0)[0].Fields()
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

func TestBuildNoSelection(t *testing.T) {
	f := mustParse(t, "[Files]\ncommon.sys=1\n[Files.2600]\nxp.sys=1\n[Files.3790]\nsrv.sys=1")
	keys := func(lines []*Line) []string {
		var out []string
		for _, l := range lines {
			out = append(out, l.Key)
		}
		return out
	}
	if got := keys(f.Section("Files", 2600)); strings.Join(got, ",") != "common.sys,xp.sys" {
		t.Errorf("build 2600 = %q", got)
	}
	if got := keys(f.Section("Files", 3790)); strings.Join(got, ",") != "common.sys,srv.sys" {
		t.Errorf("build 3790 = %q", got)
	}
	if got := keys(f.Section("Files", 0)); strings.Join(got, ",") != "common.sys" {
		t.Errorf("build 0 = %q", got)
	}
}

func TestQuotedFields(t *testing.T) {
	f := mustParse(t, `[S]
0x1,"key, with comma","he said ""hi""",tail`)
	got := f.Section("S", 0)[0].Fields()
	want := []string{"0x1", "key, with comma", `he said "hi"`, "tail"}
	if len(got) != len(want) {
		t.Fatalf("got %d fields %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTypedFields(t *testing.T) {
	f := mustParse(t, "[S]\nx=0x10,42,-3\ny=98,20,00,80")
	l := f.Section("S", 0)[0]
	if n, ok := l.IntField(0); !ok || n != 16 {
		t.Errorf("IntField(0) = %d, %v; want 16", n, ok)
	}
	if n, ok := l.IntField(2); !ok || n != -3 {
		t.Errorf("IntField(2) = %d, %v; want -3", n, ok)
	}
	bin, ok := f.Section("S", 0)[1].BinaryFields(0)
	if !ok || string(bin) != string([]byte{0x98, 0x20, 0x00, 0x80}) {
		t.Errorf("BinaryFields = %x, %v", bin, ok)
	}
}

func TestKeylessLine(t *testing.T) {
	f := mustParse(t, `[S]
"file.sif", "Section", "key", "value"`)
	l := f.Section("S", 0)[0]
	if l.Key != "" {
		t.Errorf("Key = %q, want empty", l.Key)
	}
	if got := l.Field(0); got != "file.sif" {
		t.Errorf("Field(0) = %q, want file.sif", got)
	}
}

func TestDirectiveBeforeSection(t *testing.T) {
	if _, err := Parse([]byte("orphan=1\n[S]\nx=2")); err == nil {
		t.Error("expected error for a directive before any section")
	}
}

func TestSplitFieldsEdgeCases(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{`ab"cd"`, []string{"abcd"}},                 // content before an opening quote is kept
		{`"Press a key "`, []string{"Press a key "}}, // trailing space inside quotes preserved
		{`a  ,  b  `, []string{"a", "b"}},            // unquoted surrounding whitespace trimmed
		{`a b , c`, []string{"a b", "c"}},            // interior unquoted whitespace kept
		{`"x", "y"`, []string{"x", "y"}},             // leading whitespace before a quote dropped
	}
	for _, tt := range tests {
		got := splitFields(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("splitFields(%q) = %q, want %q", tt.in, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("splitFields(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestContinuationBeforeComment(t *testing.T) {
	// A continued line carrying a trailing comment must still join the next line.
	f := mustParse(t, "[S]\nx = a, \\  ; note\n    b")
	got := f.Section("S", 0)[0].Fields()
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("fields = %q, want [a b]", got)
	}
}

func TestQuotedSemicolonAcrossContinuation(t *testing.T) {
	// A ';' inside a double-quoted value that spans a '\' continuation is part of
	// the value, not a comment — the quote state must carry across the join.
	f := mustParse(t, "[S]\nx = \"a \\\nb ; c\"")
	got := f.Section("S", 0)[0].Field(0)
	if got != "a b ; c" {
		t.Errorf("Field(0) = %q, want %q", got, "a b ; c")
	}
}

func mustParse(t *testing.T, s string) *File {
	t.Helper()
	f, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
