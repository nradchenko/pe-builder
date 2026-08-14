// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package receipt

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *Receipt {
	return &Receipt{
		Schema:  Schema,
		BuiltAt: time.Date(2026, 7, 21, 9, 14, 3, 0, time.UTC),
		PEBuild: PEBuild{Version: "v0.5.0", Commit: "1a2b3c4", EngineHash: "sha256:aa"},
		Manifest: Manifest{
			File:   "luna.hcl",
			Digest: "sha256:bb",
		},
		Source: Source{
			Identity: "build 2600 (i386)",
			File:     "winxp-sp3.iso",
			Size:     617754624,
			MTime:    time.Date(2026, 7, 17, 19, 28, 0, 0, time.UTC),
		},
		Plugins: []Plugin{
			{Name: "luna", Origin: OriginBuiltin, Options: map[string]string{"color": "NormalColor"}},
			{Name: "acme", Origin: OriginExternal, ContentHash: "sha256:cc"},
		},
		Output: Output{Target: "iso", VolumeLabel: "PEBUILDER"},
	}
}

func TestWriteAndParse(t *testing.T) {
	dir := t.TempDir()
	want := sample()
	if err := want.Write(dir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	// The receipt is meant to be read with `type` in the booted PE, where LF-only text
	// is not reliably readable.
	if bytes.Contains(bytes.ReplaceAll(data, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("receipt contains a bare LF; it must be CRLF throughout")
	}

	got, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.BuiltAt.Equal(want.BuiltAt) {
		t.Errorf("built_at = %v, want %v", got.BuiltAt, want.BuiltAt)
	}
	if got.PEBuild != want.PEBuild || got.Manifest != want.Manifest || got.Source != want.Source {
		t.Errorf("round-trip differs:\n got %+v\nwant %+v", got, want)
	}
	if len(got.Plugins) != 2 || got.Plugins[0].Name != "luna" ||
		got.Plugins[0].Options["color"] != "NormalColor" || got.Plugins[1].ContentHash != "sha256:cc" {
		t.Errorf("plugins did not round-trip: %+v", got.Plugins)
	}
	// A built-in's content is covered by the engine hash, so it carries no hash of its
	// own and the field stays out of the file entirely.
	if strings.Count(string(data), "content_hash") != 1 {
		t.Errorf("content_hash should appear once (the external plugin only):\n%s", data)
	}
}

// A receipt written by a future pebuild must be refused outright, not read as far as it
// happens to fit — a partial read would be reported as a mismatch, which is a different
// and misleading answer.
func TestParseRejectsUnknownSchema(t *testing.T) {
	r := sample()
	r.Schema = Schema + 1
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err == nil {
		t.Fatal("a receipt with an unknown schema parsed")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("garbage parsed as a receipt")
	}
}

func TestTaintLine(t *testing.T) {
	clean := sample()
	if got := clean.TaintLine(); got != "" {
		t.Errorf("a clean build has a taint line: %q", got)
	}
	dirty := sample()
	dirty.Tainted = true
	dirty.Taint = []string{Reason(TaintOutOfTree, "acme"), Reason(TaintManifestInject, "")}
	if got, want := dirty.TaintLine(), "out-of-tree:acme manifest-inject"; got != want {
		t.Errorf("taint line = %q, want %q", got, want)
	}
}

func TestReason(t *testing.T) {
	if got, want := Reason(TaintOutOfTree, "acme"), "out-of-tree:acme"; got != want {
		t.Errorf("Reason = %q, want %q", got, want)
	}
	if got, want := Reason(TaintManifestInject, ""), "manifest-inject"; got != want {
		t.Errorf("Reason with no plugin = %q, want %q", got, want)
	}
}

// The name has to survive 8.3 truncation on the mastered ISO unmangled.
func TestFileNameFits83(t *testing.T) {
	stem, ext, ok := strings.Cut(FileName, ".")
	if !ok || len(stem) > 8 || len(ext) > 3 {
		t.Errorf("%q does not fit 8.3", FileName)
	}
}
