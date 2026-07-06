// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package build is the orchestrator: it wires the pipeline stages — open source,
// probe identity, read layout, evaluate the base layer, assemble the file tree — and
// returns a report of what was produced. Registry-hive construction, boot
// configuration, and ISO mastering are later stages that slot in after tree
// assembly; at present the pipeline stops with the assembled file tree.
package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nradchenko/pe-builder/base"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/source"
	"github.com/nradchenko/pe-builder/tree"
)

// Report summarizes a build's outcome.
type Report struct {
	// Identity is the probed source identity.
	Identity source.Identity
	// OutDir is the directory the file tree was written to.
	OutDir string
	// Written is the number of files placed in the tree.
	Written int
	// Skipped names optional files absent from the source (e.g. cabinet-only drivers).
	Skipped []string
	// Errors holds non-fatal per-file failures; the build continues past them.
	Errors []error
}

// Build runs the pipeline for a validated manifest and returns its report. It fails
// (returns a non-nil error) only on a pipeline-fatal problem — an unreadable source,
// a source that is too old, or unreadable layout metadata. Per-file copy failures are
// collected in the report instead, mirroring the original tool's "number of errors"
// exit semantics.
func Build(ctx context.Context, m *manifest.Manifest) (*Report, error) {
	src, err := source.Open(m.Source.Path)
	if err != nil {
		return nil, err
	}
	id, err := src.Probe()
	if err != nil {
		return nil, fmt.Errorf("probe source: %w", err)
	}
	slog.Info("probed source", "identity", id.String())

	if enabled := m.EnabledPlugins(); len(enabled) > 0 {
		// The plugin engine — discovery, load ordering, and override layers — is a
		// later stage. Until it lands, the base layer is the whole build.
		slog.Warn("plugins are enabled but plugin evaluation is not yet wired; building base layer only",
			"count", len(enabled))
	}

	outDir, err := workDir(m)
	if err != nil {
		return nil, err
	}
	rep, err := assemble(ctx, src.Backing(), id, outDir)
	if err != nil {
		return nil, err
	}
	slog.Info("assembled file tree", "out", outDir, "written", rep.Written, "skipped", len(rep.Skipped))
	return rep, nil
}

// assemble runs the post-probe stages: layout, base-layer evaluation, and tree
// assembly. Split out from Build so it can be exercised without a real source's
// version-gated probe.
func assemble(ctx context.Context, backing source.Backing, id source.Identity, outDir string) (*Report, error) {
	l, err := layout.Parse(backing, id)
	if err != nil {
		return nil, fmt.Errorf("read source layout: %w", err)
	}
	acts, err := base.Actions(l, id)
	if err != nil {
		return nil, fmt.Errorf("evaluate base layer: %w", err)
	}

	res, err := tree.Assemble(ctx, acts, l, backing, outDir)
	if err != nil && ctx.Err() != nil {
		// A cancelled build is fatal — the tree is aborted mid-assembly — not a
		// per-file error to be reported and shrugged off.
		return nil, ctx.Err()
	}
	rep := &Report{
		Identity: id,
		OutDir:   outDir,
		Written:  len(res.Written),
		Skipped:  res.Skipped,
	}
	if err != nil {
		rep.Errors = append(rep.Errors, err)
	}
	return rep, nil
}

// workDir returns the directory the tree is written to: the manifest's work dir when
// set (created if needed), otherwise a fresh system temp directory. The assembled
// tree is the build's deliverable at this stage, so it is never removed here.
func workDir(m *manifest.Manifest) (string, error) {
	if dir := m.Build.WorkDir; dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create work dir %q: %w", dir, err)
		}
		return dir, nil
	}
	dir, err := os.MkdirTemp("", "pebuild-*")
	if err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	return dir, nil
}
