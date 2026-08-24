// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package receipt records what a build was made from. A build writes a receipt into
// the tree it assembles, so an image on disk can answer "was this built by the pebuild
// I have now, from this manifest?" without booting it or picking it apart.
//
// The receipt describes the build's *inputs* — the engine content, the manifest, the
// Windows source, and the resolved plugin set with the option values it settled on —
// together with the build's taint verdict. It deliberately does not describe the image
// it travels inside: it lives in that image, so it cannot hash it.
//
// It is written from the resolved build plan and is never read back during a build.
// Nothing may make it a build input, or verifying a build would be circular.
package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is the receipt's name at the root of a built tree. The ISO stage uppercases
// the whole tree, so it is PEBUILD.TXT on a mastered image. The name is 7+3, so it
// survives 8.3 truncation unmangled; a longer one would not. The .txt extension is for
// the environment it is read in — a booted PE can `type X:\PEBUILD.TXT`.
const FileName = "pebuild.txt"

// Schema is the receipt format version, so a reader can reject a receipt it does not
// understand rather than misreport a build.
const Schema = 1

// Origin values for a plugin entry.
const (
	// OriginBuiltin marks a plugin embedded in pebuild itself. Its content is covered
	// by the engine hash, so it carries no content hash of its own.
	OriginBuiltin = "builtin"
	// OriginExternal marks a plugin from a drop-in directory. It is not embedded, so
	// its entry carries its own content hash.
	OriginExternal = "external"
)

// Receipt is the record a build leaves in the tree it produced.
type Receipt struct {
	Schema  int       `json:"schema"`
	BuiltAt time.Time `json:"built_at"`

	PEBuild  PEBuild  `json:"pebuild"`
	Manifest Manifest `json:"manifest"`
	Source   Source   `json:"source"`
	Plugins  []Plugin `json:"plugins"`
	Output   Output   `json:"output"`

	// Tainted reports whether the build used inputs outside the vetted, in-tree set,
	// and Taint says by what. See Reasons.
	Tainted bool     `json:"tainted"`
	Taint   []string `json:"taint"`
}

// PEBuild identifies the engine that ran the build.
type PEBuild struct {
	// Version and Commit are the link-time stamps; they read "dev"/"none" when the
	// build ran unstamped (go run, go test), which is not a release.
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// EngineHash is the content hash of pebuild's embedded trees — the base layer and
	// the built-in plugins. It is what makes this record survive a no-op rebuild: the
	// executable's bytes change every time it is linked, its embedded content does not.
	EngineHash string `json:"engine_hash"`
}

// Manifest identifies the build manifest. File is informational; Digest is the identity,
// so a manifest that was moved or renamed still matches.
//
// File is the manifest's base name, never the path it was read from: a receipt ships
// inside the image, so anything recorded here travels to whoever receives that image,
// and the directory layout of the machine that built it is nobody else's business. The
// name is what a human reads to recognize the build; the digest is what a machine
// compares. Neither needs a path.
type Manifest struct {
	File   string `json:"file,omitempty"`
	Digest string `json:"sha256"`
}

// Source fingerprints the Windows installation source. It is identity plus size and
// mtime rather than a content hash: cheap and instant, it catches a swap to different
// media, and it knowingly does not catch an edited source of identical size and
// timestamp.
//
// File is the media's base name only, for the same reason as Manifest.File — and the
// name carries the useful half anyway (which ISO), where the directory it sat in says
// only where the builder keeps their files.
type Source struct {
	Identity string    `json:"identity"`
	File     string    `json:"file,omitempty"`
	Size     int64     `json:"size"`
	MTime    time.Time `json:"mtime"`
}

// Plugin is one entry of the fully resolved plugin set — auto-enabled dependencies
// included, in the order the engine settled on — with the option values it was built
// with, so "was this built at 1280x1024?" is answerable without booting.
type Plugin struct {
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// ContentHash fingerprints a plugin pebuild does not embed; it is empty for a
	// built-in, whose content the engine hash already covers.
	ContentHash string            `json:"content_hash,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
}

// Output describes the artifact the build was asked to produce.
type Output struct {
	Target      string `json:"target"`
	VolumeLabel string `json:"volume_label,omitempty"`
}

// Write places the receipt at the root of a built tree. It is written for a directory
// output too, so a tree can be verified without mastering an ISO.
//
// The bytes are CRLF-terminated: the file exists to be read in the environment it ships
// to, and `type` under cmd.exe is only reliably readable that way.
func (r *Receipt) Write(treeDir string) error {
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(treeDir, FileName), data, 0o644); err != nil {
		return fmt.Errorf("write build receipt: %w", err)
	}
	return nil
}

// Marshal renders the receipt as it is written: pretty-printed JSON with CRLF line
// endings and a trailing newline.
func (r *Receipt) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode build receipt: %w", err)
	}
	data = append(data, '\n')
	return bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")), nil
}

// Parse decodes a receipt's bytes. A receipt whose schema this pebuild does not know is
// rejected rather than read as far as it happens to fit, so a future format cannot be
// misreported as a mismatch.
func Parse(data []byte) (*Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse build receipt: %w", err)
	}
	if r.Schema != Schema {
		return nil, fmt.Errorf("build receipt has schema %d, which this pebuild does not understand (it writes schema %d)", r.Schema, Schema)
	}
	return &r, nil
}

// Taint reason kinds. A build is tainted when it used inputs outside the vetted,
// in-tree set — pebuild's embedded built-ins and its base layer. The reasons are graded
// rather than a bare boolean, so a provenance question can be triaged by what actually
// happened. They record; they never gate: no build is refused for being tainted, and the
// build.allow_plugin_builds trust gate remains the guardrail that decides what may run.
const (
	// TaintOutOfTree names a plugin that is not one of pebuild's built-ins.
	TaintOutOfTree = "out-of-tree"
	// TaintRanBuildCode names an out-of-tree plugin whose [Build] section ran, i.e.
	// arbitrary commands executed on the build host.
	TaintRanBuildCode = "ran-build-code"
	// TaintShippedBinaries names an out-of-tree plugin that staged files it carried:
	// bytes of unknown provenance in the image, as opposed to files taken from the
	// Windows source.
	TaintShippedBinaries = "shipped-binaries"
	// TaintManifestInject marks a manifest that placed host files straight into the
	// image with inject{}. It is the same provenance concern, but the user's own
	// explicit choice, so it is its own reason rather than blamed on a plugin.
	TaintManifestInject = "manifest-inject"
)

// Reason renders one taint reason: "<kind>" on its own, or "<kind>:<plugin>" when the
// reason is attributable to a specific plugin.
func Reason(kind, plugin string) string {
	if plugin == "" {
		return kind
	}
	return kind + ":" + plugin
}

// TaintLine renders the taint reasons the way the build log states them, and is empty
// for a clean build — which logs no taint line at all.
func (r *Receipt) TaintLine() string {
	if !r.Tainted {
		return ""
	}
	return strings.Join(r.Taint, " ")
}
