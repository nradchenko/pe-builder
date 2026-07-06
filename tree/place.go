// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"fmt"
	"io/fs"
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
func placeFile(a action.Action, l *layout.Layout, src source.Backing, outRoot string) (written, skipped string, err error) {
	name := strings.ToLower(a.Src)

	data, compressed, found := readSource(src, name)
	if !found {
		if a.Optional {
			return "", name, nil
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
	}

	rel := outputPath(a, l, outName)
	full := filepath.Join(outRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", "", fmt.Errorf("create dir for %q: %w", name, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", "", fmt.Errorf("write %q: %w", rel, err)
	}
	return rel, "", nil
}

// readSource reads a source file, trying its loose (uncompressed) name first and its
// compressed *.??_ name second. It reports the bytes, whether they came from the
// compressed form, and whether the file was found at all.
func readSource(src source.Backing, name string) (data []byte, compressed, found bool) {
	loose := path.Join(systemRoot, name)
	if b, err := fs.ReadFile(src, loose); err == nil {
		return b, false, true
	}
	packed := path.Join(systemRoot, layout.CompressedName(name))
	if b, err := fs.ReadFile(src, packed); err == nil {
		return b, true, true
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

// outputPath computes the output-relative (forward-slash) path for a placed file:
// the system root, the file's target directory, then the file name. The directory
// is taken from the action when it specifies one, else resolved from the source
// layout, else defaulted to system32.
func outputPath(a action.Action, l *layout.Layout, outName string) string {
	dirID := a.DirID
	if dirID == "" {
		if e, ok := l.Resolve(a.Src); ok {
			dirID = e.TargetDir
		}
	}
	rel := dirPath(dirID, "", l)
	return path.Join(systemRoot, rel, outName)
}

// dirPath resolves a directory ID to its system-root-relative path. An explicit
// path (from a Mkdir action's [WinntDirectories] entry) is used directly; otherwise
// the ID is looked up in the layout. An unknown ID falls back to system32 so a
// misresolved file is still placed somewhere predictable rather than at the root.
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
	return "system32"
}
