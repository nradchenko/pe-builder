// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package build is the orchestrator: it wires the pipeline stages — open source,
// probe identity, read layout, evaluate the base layer, assemble the file tree,
// build the registry hives, and apply the boot configuration — and returns a report
// of what was produced. Boot configuration (the txtsetup.sif /minint edit and the
// startup script) is tree configuration applied for every target, so a "none" build
// yields a complete PE tree; only the boot image and the ISO itself are media
// stages, run for the iso target.
package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/base"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/inifile"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/source"
	"github.com/nradchenko/pe-builder/target"
	"github.com/nradchenko/pe-builder/tree"
)

// Report summarizes a build's outcome.
type Report struct {
	// Identity is the probed source identity.
	Identity source.Identity
	// OutDir is the directory the file tree was written to. It is consumed by ISO
	// mastering unless the build keeps its work directory.
	OutDir string
	// Written is the number of files placed in the tree.
	Written int
	// Skipped names optional files absent from the source (e.g. cabinet-only drivers).
	Skipped []string
	// Hives lists the output-relative paths of the registry hives written.
	Hives []string
	// ISOPath, ISOSize, and VolumeLabel describe the mastered image; ISOPath is empty
	// when the target produced no media.
	ISOPath     string
	ISOSize     int64
	VolumeLabel string
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

	// A bootable image reuses the source ISO's own boot sector, so reject a directory
	// source before assembling the whole tree rather than at the mastering step.
	if m.Output.Target == manifest.TargetISO {
		if err := source.RequireImageSource(m.Source.Path); err != nil {
			return nil, fmt.Errorf("bootable output requires an iso source: %w", err)
		}
	}

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

	// Master the ISO only when the tree assembled cleanly: per-file errors would
	// produce a broken image, and mastering consumes the tree, so leave it for
	// inspection instead. The per-file failures already set a non-zero exit.
	if m.Output.Target == manifest.TargetISO && len(rep.Errors) == 0 {
		if err := emitISO(ctx, m, outDir, rep); err != nil {
			return nil, err
		}
		if !m.Build.KeepWork {
			// Mastering consumed the tree; it survives only when kept for inspection.
			rep.OutDir = ""
		}
		slog.Info("mastered iso", "path", rep.ISOPath, "bytes", rep.ISOSize)
	}
	return rep, nil
}

// assemble runs the post-probe tree stages: layout, base-layer evaluation, tree
// assembly, hive construction, and boot configuration. Split out from Build so it
// can be exercised without a real source's version-gated probe. Boot configuration
// runs here — not gated on the media target — so the produced tree is a complete PE
// tree regardless of whether an ISO is then mastered from it.
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

	// Build the registry hives from the source's base hive material overlaid with
	// the registry actions, and place them in the tree's config folder. This is a
	// pipeline-fatal step: a build with no loadable SYSTEM hive cannot boot. It also
	// honors cancellation, so a build cancelled just as tree assembly finished does
	// not run the full hive read/serialize/write and report success.
	hives, err := buildHives(ctx, backing, l, acts, outDir)
	if err != nil {
		return nil, fmt.Errorf("build registry hives: %w", err)
	}
	rep.Hives = hives
	slog.Info("built registry hives", "count", len(hives))

	if err := configure(outDir, acts, res.Written); err != nil {
		return nil, err
	}

	// Copy the disc identity markers (WIN51*) and boot font from the source root to
	// the tree root, where the loader looks for them to confirm the disc.
	if err := copyIdentityMarkers(backing, outDir); err != nil {
		return nil, fmt.Errorf("copy identity markers: %w", err)
	}
	return rep, nil
}

// configure applies the boot configuration to the assembled tree: the text edits
// (chiefly txtsetup.sif's /minint switch, applied wherever the copy stage placed
// each target) and the built-in startup script. It is tree configuration, not media
// mastering, so it runs for every target.
func configure(treeDir string, acts []action.Action, written []string) error {
	if err := inifile.Apply(acts, treeDir, inifile.Index(written)); err != nil {
		return fmt.Errorf("apply boot configuration: %w", err)
	}
	if err := boot.WriteStartnet(treeDir, base.StartnetCmd()); err != nil {
		return err
	}
	return nil
}

// emitISO is the media tail, run only for the iso target: it extracts the source
// ISO's boot image and hands the tree to the iso backend. Mastering consumes the
// tree, so when the build keeps its work directory the ISO is built from a clone.
// A future media target would extract its own boot image here and reuse the shared
// boot configuration applied in assemble.
func emitISO(ctx context.Context, m *manifest.Manifest, treeDir string, rep *Report) error {
	img, err := source.OpenImageFile(m.Source.Path)
	if err != nil {
		return err
	}
	n, err := boot.WriteBootImage(img, treeDir)
	_ = img.Close()
	if err != nil {
		return err
	}
	slog.Info("extracted boot image", "bytes", n)

	workspace := treeDir
	if m.Build.KeepWork {
		// Mastering removes its workspace; build from a clone so the work directory
		// survives. The clone is dropped here too, in case mastering fails before it
		// would have removed it.
		workspace, err = cloneTree(treeDir)
		if err != nil {
			return err
		}
		defer os.RemoveAll(workspace)
	}

	tgt, ok := target.Get(m.Output.Target)
	if !ok {
		return fmt.Errorf("unknown output target %q", m.Output.Target)
	}
	if err := tgt.Emit(ctx, workspace, target.Options{
		OutputPath:  m.Output.Path,
		VolumeLabel: m.Output.VolumeLabel,
	}); err != nil {
		// Do not leave a half-written image at the output path.
		_ = os.Remove(m.Output.Path)
		return err
	}

	info, err := os.Stat(m.Output.Path)
	if err != nil {
		return fmt.Errorf("stat output iso: %w", err)
	}
	rep.ISOPath = m.Output.Path
	rep.ISOSize = info.Size()
	rep.VolumeLabel = m.Output.VolumeLabel
	return nil
}

// workDir returns the directory the tree is written to: the manifest's work dir when
// set (created if needed), otherwise a fresh system temp directory.
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
