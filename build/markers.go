// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/source"
)

// bootFontName is the console-font table the loader maps to render non-Latin boot
// text; without it the boot screens are garbled on a localized source.
const bootFontName = "bootfont.bin"

// copyIdentityMarkers copies the Windows CD identity files from the source root to
// the output-tree root. The loader checks the WIN51* marker files (empty stamps
// like WIN51, WIN51IP, WIN51IP.SP3 that name the edition and service pack) to
// confirm it is reading a genuine Windows disc; without them it stops and prompts
// for the CD. The boot font is copied alongside them. These live at the disc root,
// outside the system-root (I386) tree the file stage assembles.
// It returns the output-tree-relative paths it wrote (the root marker names), so a caller
// that masters a separate bootable volume — the wim-cd coreimg, itself the disc setupldr
// validates — can carry the same markers.
func copyIdentityMarkers(backing source.Backing, outDir string) ([]string, error) {
	entries, err := fs.ReadDir(backing, ".")
	if err != nil {
		return nil, fmt.Errorf("read source root: %w", err)
	}
	var written []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isIdentityMarker(name) {
			continue
		}
		data, err := fs.ReadFile(backing, name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, name), data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		written = append(written, name)
	}
	return written, nil
}

// isIdentityMarker reports whether a source-root file is a disc-identity marker
// (a WIN51 stamp) or the boot font — the root files the boot chain needs.
func isIdentityMarker(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), "WIN51") || strings.EqualFold(name, bootFontName)
}
