// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/internal/base"
	"github.com/nradchenko/pe-builder/internal/buildinfo"
	"github.com/nradchenko/pe-builder/internal/enginehash"
	"github.com/nradchenko/pe-builder/internal/manifest"
	"github.com/nradchenko/pe-builder/internal/plugin"
	"github.com/nradchenko/pe-builder/internal/receipt"
	"github.com/nradchenko/pe-builder/plugins"
	"github.com/nradchenko/pe-builder/source"
)

// makeReceipt composes the record of what the build was made from: the engine content,
// the manifest, the source, the fully resolved plugin set with its effective options,
// and the taint verdict computed from the same set.
//
// It is derived entirely from the resolved plan — nothing here reads the assembled tree,
// and nothing in the build reads the receipt back.
func makeReceipt(m *manifest.Manifest, id source.Identity, selected []plugin.Selected, builtAt time.Time) (*receipt.Receipt, error) {
	// The base layer and the built-in plugins are what is semantically baked into
	// pebuild, hashed in a fixed order so the digest is reproducible.
	engine, err := enginehash.Sum(base.EmbedFS(), plugins.FS)
	if err != nil {
		return nil, err
	}

	pl, err := receiptPlugins(selected)
	if err != nil {
		return nil, err
	}
	reasons := taintReasons(m, selected)

	return &receipt.Receipt{
		Schema:  receipt.Schema,
		BuiltAt: builtAt.UTC().Truncate(time.Second),
		PEBuild: receipt.PEBuild{
			Version:    buildinfo.Version,
			Commit:     buildinfo.Commit,
			EngineHash: engine,
		},
		Manifest: receipt.Manifest{
			File:   baseName(m.Path),
			Digest: m.Digest,
		},
		Source:  sourceFingerprint(m.Source.Path, id),
		Plugins: pl,
		Output: receipt.Output{
			Target:      m.Output.Target,
			VolumeLabel: m.Output.VolumeLabel,
		},
		Tainted: len(reasons) > 0,
		Taint:   reasons,
	}, nil
}

// receiptPlugins records the resolved plugin set in build order, each with the option
// values it was compiled with. A plugin pebuild does not embed carries a content hash of
// its own, since the engine hash cannot cover it.
func receiptPlugins(selected []plugin.Selected) ([]receipt.Plugin, error) {
	out := make([]receipt.Plugin, 0, len(selected))
	for _, s := range selected {
		opts, err := plugin.EffectiveOptions(s)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", s.Plugin.Name, err)
		}
		e := receipt.Plugin{
			Name:    s.Plugin.Name,
			Origin:  receipt.OriginBuiltin,
			Options: opts,
		}
		if !isBuiltin(s) {
			e.Origin = receipt.OriginExternal
			if s.Plugin.FS != nil {
				// A plugin directory is a place on someone's disk: it can hold a dangling
				// symlink, a socket, or a file the build cannot read, none of which the
				// build itself cared about. Recording no hash is a small loss — verify
				// reports the field as UNKNOWN rather than claiming a match — while
				// failing here would throw away an assembled tree at the last step over a
				// file that contributed nothing to it.
				h, err := enginehash.Sum(s.Plugin.FS)
				if err != nil {
					slog.Warn("cannot fingerprint plugin directory; the receipt will record no content hash for it",
						"plugin", s.Plugin.Name, "error", err)
				} else {
					e.ContentHash = h
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// sourceFingerprint records the Windows source as identity plus size and mtime, and its
// base name — not the path it was read from, which would put the build machine's
// directory layout inside every image built from it (see receipt.Source). A stat that
// fails is not a build error: the source was read successfully moments ago, and a receipt
// with an unsized source is more useful than a failed build.
func sourceFingerprint(path string, id source.Identity) receipt.Source {
	s := receipt.Source{Identity: id.String(), File: baseName(path)}
	if info, err := os.Stat(path); err == nil {
		s.Size = info.Size()
		s.MTime = info.ModTime().UTC().Truncate(time.Second)
	}
	return s
}

// baseName reduces a local path to the file name a receipt may carry. It is the one
// place that redaction happens, so no field can pick up a full path by being added
// without thinking about it.
func baseName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// isBuiltin reports whether a selected plugin is one pebuild embeds. That is the trust
// boundary taint is drawn at: the built-ins and the base layer ship with the release and
// reproduce from source, so they are in-tree. A built-in build-capable plugin runs
// [Build] code and still counts as clean — taint is about provenance, not about whether
// code ran.
func isBuiltin(s plugin.Selected) bool { return s.Plugin.Source == plugin.BuiltinLabel }

// taintReasons computes the graded taint reasons for a build, sorted so a receipt is
// byte-stable across runs of the same build. An empty result is a clean build; it is
// empty rather than nil so a clean receipt records "taint": [] — an explicit "nothing",
// which reads unambiguously, where null could be mistaken for "not computed".
func taintReasons(m *manifest.Manifest, selected []plugin.Selected) []string {
	reasons := []string{}
	for _, s := range selected {
		if isBuiltin(s) {
			continue
		}
		name := s.Plugin.Name
		reasons = append(reasons, receipt.Reason(receipt.TaintOutOfTree, name))
		// Built is set only when the [Build] pre-pass actually ran for this plugin, so
		// this reports what happened rather than what the plugin declares.
		if s.Built != nil {
			reasons = append(reasons, receipt.Reason(receipt.TaintRanBuildCode, name))
		}
		if shipsOwnFiles(s) {
			reasons = append(reasons, receipt.Reason(receipt.TaintShippedBinaries, name))
		}
	}
	// inject{} is the manifest's own doing, not a plugin's, even though it is written
	// inside a plugin block.
	for _, p := range m.EnabledPlugins() {
		if len(p.Files.Inject) > 0 {
			reasons = append(reasons, receipt.Reason(receipt.TaintManifestInject, ""))
			break
		}
	}
	sort.Strings(reasons)
	return reasons
}

// shipsOwnFiles reports whether a plugin stages files it carries in its own directory
// (a [SourceDisksFiles] entry marked as shipping with the plugin), as opposed to files
// copied from the Windows source or produced by its [Build].
func shipsOwnFiles(s plugin.Selected) bool {
	for _, a := range s.Plugin.Actions() {
		if a.Kind == action.CopyFile && a.ShipWithPlugin {
			return true
		}
	}
	return false
}
