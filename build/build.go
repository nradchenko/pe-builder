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
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/base"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/inifile"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/plugin"
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

	selected, err := selectPlugins(m, id.Build)
	if err != nil {
		return nil, err
	}

	outDir, err := workDir(m)
	if err != nil {
		return nil, err
	}

	// Compile the build-capable plugins' payloads before assembly, so their produced
	// files are available as origins when the tree is placed. This runs after workDir,
	// which creates a configured work_dir the plugin build directories nest under. The
	// build directories are cleaned up once the tree (which reads them) is assembled.
	cleanupBuilds, err := runPluginBuilds(ctx, selected, m)
	if err != nil {
		return nil, err
	}
	defer cleanupBuilds()

	rep, err := assemble(ctx, src.Backing(), id, outDir, selected)
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
//
// selected are the enabled plugins in load order (empty when none is enabled), paired
// with their manifest override layer. They are compiled to actions here — where the
// source layout is available, so files.exclude can match a copy by its resolved output
// path — and apply after the base layer, last-writer-wins. Their file origins let the
// tree assembler read a file a plugin ships from the plugin rather than the source.
func assemble(ctx context.Context, backing source.Backing, id source.Identity, outDir string, selected []plugin.Selected) (*Report, error) {
	l, err := layout.Parse(backing, id)
	if err != nil {
		return nil, fmt.Errorf("read source layout: %w", err)
	}
	acts, err := base.Actions(l, id)
	if err != nil {
		return nil, fmt.Errorf("evaluate base layer: %w", err)
	}
	// The base layer is not a plugin; the enabled plugins layer on top of it, in load
	// order, so a later plugin's write wins over the base and earlier plugins. Plugin
	// copies resolve their output path through the same layout the tree uses, so a
	// manifest exclude can suppress one by its destination.
	pluginActs, origins, err := plugin.Compile(selected, func(a action.Action) string {
		return tree.OutputPath(a, l)
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate plugins: %w", err)
	}
	acts = append(acts, pluginActs...)

	res, err := tree.Assemble(ctx, acts, l, backing, origins, outDir)
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

// selectPlugins resolves the manifest's enabled plugins into their compiled actions
// (in load order) and the file-origin map for their shipped files. It returns nils
// when no plugin is enabled — the base layer is then the whole build.
//
// A plugin in the search path that fails to parse does not fail the build unless it
// is one the manifest enabled: Select errors clearly when an enabled plugin is
// missing (whether absent or unparseable), and an unrelated broken plugin is only
// warned about.
func selectPlugins(m *manifest.Manifest, build int) ([]plugin.Selected, error) {
	enabled := m.EnabledPlugins()
	if len(enabled) == 0 {
		return nil, nil
	}

	sp, err := plugin.DefaultSearchPath(m.PluginsDir)
	if err != nil {
		return nil, err
	}
	discovered, discErr := plugin.Discover(sp, build)

	names := make([]string, len(enabled))
	for i, p := range enabled {
		names[i] = p.Name
	}
	selected, err := plugin.Select(discovered, names)
	if err != nil {
		// An enabled plugin is missing. If some plugins also failed to parse, the
		// cause may be one of them, so include that detail.
		if discErr != nil {
			return nil, fmt.Errorf("%w (%v)", err, discErr)
		}
		return nil, err
	}
	if discErr != nil {
		slog.Warn("some plugins in the search path failed to parse; enabled plugins are unaffected", "error", discErr)
	}

	plugin.Order(selected)
	if err := plugin.ValidateDepends(selected); err != nil {
		return nil, err
	}
	warnUnappliedAppends(selected)

	// Pair each selected plugin with its manifest override layer (by name).
	overrides := make(map[string]manifest.Plugin, len(enabled))
	for _, p := range enabled {
		overrides[strings.ToLower(p.Name)] = p
	}
	out := make([]plugin.Selected, len(selected))
	for i, d := range selected {
		out[i] = plugin.Selected{Plugin: d, Override: overrideLayer(overrides[strings.ToLower(d.Name)])}
	}
	slog.Info("selected plugins", "count", len(out))
	return out, nil
}

// runPluginBuilds runs the [Build] pre-pass for every build-capable selected plugin,
// populating each Selected.Built, and returns a cleanup for the temporary build
// directories (a no-op when nothing was built). A built-in (embedded) plugin builds
// automatically; a user-supplied plugin's [Build] runs an arbitrary command, so it is
// refused unless the manifest opts in with build.allow_plugin_builds.
func runPluginBuilds(ctx context.Context, selected []plugin.Selected, m *manifest.Manifest) (func(), error) {
	cleanup := func() {}
	var buildRoot string

	for i := range selected {
		d := selected[i].Plugin
		if !d.Build().Present {
			continue
		}
		if d.Source != plugin.BuiltinLabel && !m.Build.AllowPluginBuilds {
			cleanup()
			return nil, fmt.Errorf("plugin %q declares a [Build] section, which runs an arbitrary command; "+
				"set build.allow_plugin_builds = true to allow user-supplied plugin builds", d.Name)
		}
		if buildRoot == "" {
			// A configured work_dir may not exist yet; create it so the temp build root
			// nests under it regardless of call order.
			if m.Build.WorkDir != "" {
				if err := os.MkdirAll(m.Build.WorkDir, 0o755); err != nil {
					return nil, err
				}
			}
			root, err := os.MkdirTemp(m.Build.WorkDir, "pebuild-plugin-builds-*")
			if err != nil {
				return nil, err
			}
			buildRoot = root
			cleanup = func() { os.RemoveAll(buildRoot) }
		}
		built, err := plugin.RunBuild(ctx, d, buildRoot)
		if err != nil {
			cleanup()
			return nil, err
		}
		selected[i].Built = built
		slog.Info("built plugin payload", "plugin", d.Name, "produces", len(d.Build().Produces))
	}
	return cleanup, nil
}

// overrideLayer maps a manifest plugin block's file/registry overrides to the plugin
// engine's Override type.
func overrideLayer(p manifest.Plugin) plugin.Override {
	ov := plugin.Override{
		Include:   p.Files.Include,
		Exclude:   p.Files.Exclude,
		RegSet:    p.Registry.Set,
		RegDelete: p.Registry.Delete,
	}
	for _, in := range p.Files.Inject {
		ov.Inject = append(ov.Inject, plugin.Inject{From: in.From, To: in.To})
	}
	return ov
}

// warnUnappliedAppends warns for each enabled plugin that uses [Append] (the Nu2Menu
// XML merge), which pebuild parses but deliberately does not apply, so the omission
// is visible rather than silent.
func warnUnappliedAppends(selected []*plugin.Discovered) {
	for _, d := range selected {
		for _, a := range d.Actions() {
			if a.Kind == action.Append {
				slog.Warn("plugin uses [Append] (Nu2Menu XML), which pebuild does not apply; skipping it",
					"plugin", d.Name, "target", a.TargetFile)
				break
			}
		}
	}
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
