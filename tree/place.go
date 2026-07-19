// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/compress"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// placeFile resolves, reads, decompresses, and writes one copy action. It returns
// the output-relative path written, or — when the file is optional and absent from
// the source — the canonical name to record as skipped. Exactly one of the two
// strings is non-empty on success.
func placeFile(a action.Action, l *layout.Layout, src source.Backing, roots source.Roots, cabs *cabinets, origins map[string]fs.FS, outRoot string) (written, skipped string, err error) {
	name := strings.ToLower(a.Src)

	data, compressed, found := readFile(a, origins, src, roots, cabs, name)
	if !found {
		if a.Optional {
			return "", name, nil
		}
		// A file the plugin promised to ship (Origin set, ShipWithPlugin) but did not
		// is the plugin's fault, not a missing Windows-source file — say so.
		if a.Origin != "" {
			return "", "", fmt.Errorf("plugin %q does not ship required file %q", a.Origin, a.Src)
		}
		// A [SourcePaths] copy addresses the source by an exact path (Src is empty), so
		// name it by that path rather than the empty basename.
		if a.SrcPath != "" {
			return "", "", fmt.Errorf("required source file %q not found", a.SrcPath)
		}
		return "", "", fmt.Errorf("required source file %q not found", name)
	}

	// A compressed archive is decompressed unless the action says to copy it as-is
	// (attribute 2), in which case the compressed bytes are written under the
	// compressed name.
	outName := targetName(a, name)
	if compressed {
		if a.NoDecompress {
			outName = layout.CompressedName(outName)
		} else {
			data, err = compress.DecompressBytes(data)
			if err != nil {
				return "", "", fmt.Errorf("decompress %q: %w", name, err)
			}
		}
	} else if a.NoDecompress && a.Origin == "" {
		// The file was found already-decompressed (a loose file or a cabinet member),
		// so there is no compressed archive to preserve — attribute 2 cannot apply.
		// A plugin-shipped file is always final, so attribute 2 on it is simply a
		// no-op (write as-is), not a lost packed form worth warning about.
		slog.Warn("cannot keep file compressed; source has no packed archive form", "file", name)
	}

	// A manifest include/inject override names its target path directly; otherwise the
	// directory is resolved from the action's ID/path and the source layout.
	rel := a.Dest
	if rel == "" {
		rel = outputPath(a, l, roots, outName)
	}
	full := filepath.Join(outRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", "", fmt.Errorf("create dir for %q: %w", name, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", "", fmt.Errorf("write %q: %w", rel, err)
	}
	return rel, "", nil
}

// readFile resolves a copy action's bytes by origin. An Origin is set only for a file
// the plugin ships itself, so those bytes come from the origin's fs.FS and never from
// the Windows source: a miss is a not-found (the plugin failed to ship a file it
// promised), not a silent fall-through that could pick up an unrelated source file of
// the same name. Everything else resolves from the Windows source. Origin bytes are
// already final (never a *.??_ archive), so they never need decompression.
func readFile(a action.Action, origins map[string]fs.FS, src source.Backing, roots source.Roots, cabs *cabinets, name string) (data []byte, compressed, found bool) {
	if a.Origin != "" {
		if fsys, ok := origins[a.Origin]; ok {
			if b, ok := readOrigin(fsys, a.Src); ok {
				return b, false, true
			}
		}
		return nil, false, false
	}
	// A [SourcePaths] copy names an exact source path (a file whose basename is not
	// unique): read it directly, still honoring the compressed .??_ form, rather than
	// resolving a basename that would find the wrong file.
	// The area is pinned to the system payload here and in outputPath, the two halves
	// of one rule: a file is read from and written to the same area, never a mix. They
	// are the pair to change together when an action carries its own area.
	if a.SrcPath != "" {
		return layout.ReadSourceFile(src, roots, source.AreaSystem, a.SrcPath)
	}
	return readSource(src, roots, cabs, name)
}

// readOrigin reads a shipped file from a plugin/build-output fs.FS. It tries the name
// as the action spells it, then its lowercased form, so a case difference between the
// [SourceDisksFiles] entry and the shipped file name does not miss the file.
func readOrigin(fsys fs.FS, src string) ([]byte, bool) {
	if b, err := fs.ReadFile(fsys, src); err == nil {
		return b, true
	}
	if lower := strings.ToLower(src); lower != src {
		if b, err := fs.ReadFile(fsys, lower); err == nil {
			return b, true
		}
	}
	return nil, false
}

// readSource reads a source file, trying in order: the shared loose→*.??_ archive
// resolution (layout.ReadSourceFile), then the driver cabinets (DRIVER.CAB /
// SP*.CAB), where many class and HID drivers ship. It reports the bytes, whether
// they still need decompression (only the *.??_ archive form does; loose and
// cabinet-extracted bytes are already final), and whether the file was found.
func readSource(src source.Backing, roots source.Roots, cabs *cabinets, name string) (data []byte, compressed, found bool) {
	if b, packed, ok := layout.ReadSourceFile(src, roots, source.AreaSystem, name); ok {
		return b, packed, true
	}
	if cabs != nil {
		if b, ok := cabs.Find(name); ok {
			return b, false, true
		}
	}
	return nil, false, false
}

// targetName is the file's output name: its rename target if the action renames it,
// otherwise its canonical name. Casing is left as-is (see the package doc).
func targetName(a action.Action, name string) string {
	if a.Rename != "" {
		return strings.ToLower(a.Rename)
	}
	return name
}

// OutputPath returns the output-tree-relative (forward-slash) path a copy action
// resolves to — the same path Assemble writes it to. It lets a caller match a copy
// against a target path (e.g. a manifest exclude entry) without re-deriving the
// placement rules. A copy that names its destination directly (Dest) returns it
// verbatim.
func OutputPath(a action.Action, l *layout.Layout, roots source.Roots) string {
	if a.Dest != "" {
		return a.Dest
	}
	return outputPath(a, l, roots, targetName(a, strings.ToLower(a.Src)))
}

// outputPath computes the output-relative (forward-slash) path for a placed file:
// the file's target directory then its name, under the system root unless the
// directory is created from the output root. The directory comes from the action's
// own path when it carries one (a plugin-defined [WinntDirectories] entry), else its
// directory ID resolved from the source layout, else the source layout keyed by file
// name, else system32.
func outputPath(a action.Action, l *layout.Layout, roots source.Roots, outName string) string {
	dirID := a.DirID
	if dirID == "" && a.Path == "" {
		if e, ok := l.Resolve(a.Src); ok {
			dirID = e.TargetDir
		}
	}
	rel := dirPath(dirID, a.Path, l)
	if a.FromRoot {
		return path.Join(rel, outName)
	}
	// The write half of the read/write area pair pinned in readFile; see the note
	// there. Both must move together.
	return path.Join(roots.For(source.AreaSystem), rel, outName)
}

// rootDirID is the [WinntDirectories] directory ID for the system root (mapped to
// "\" universally in a source's txtsetup.sif), where the boot files are pinned.
const rootDirID = "1"

// dirPath resolves a directory ID to its system-root-relative path. An explicit
// path (from a Mkdir action's [WinntDirectories] entry) is used directly; otherwise
// the ID is looked up in the layout. When the ID is the well-known system-root ID,
// it maps to the root even if a source omits it from its table — so a file pinned
// to the root (the boot loader) is not silently relocated. Any other unknown ID
// falls back to system32, so a misresolved file still lands somewhere predictable.
func dirPath(dirID, explicit string, l *layout.Layout) string {
	if explicit != "" {
		// A [WinntDirectories] path uses backslashes; normalize to the forward-slash
		// form used elsewhere in the tree. filepath.ToSlash is not enough — it is a
		// no-op on non-Windows hosts, where the path separator is already '/'.
		return strings.Trim(strings.ReplaceAll(explicit, `\`, "/"), "/")
	}
	if p, ok := l.DirPath(dirID); ok {
		return p
	}
	if dirID == rootDirID {
		return "" // the system root
	}
	return "system32"
}
