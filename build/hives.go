// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/hive"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// configDirID is the source's [WinntDirectories] directory ID for the registry
// config folder (system32\config), where the built hives are placed.
const configDirID = "3"

// configFallback is the config directory relative to the system root, used when
// the source metadata does not carry directory ID 3.
const configFallback = "system32/config"

// buildHives constructs the base hives from the source, overlays the registry
// actions, and writes SYSTEM, SOFTWARE, and DEFAULT into the tree's config
// folder (system32\config under the system root). It returns the output-relative
// paths written, for the report.
//
// The kernel loads the SYSTEM hive from this folder at boot; SOFTWARE and the
// default-user hive are loaded by the session/logon chain. The CLASSES hive the
// engine also builds is not written here — a bare boot to cmd.exe does not need
// HKCR, and how it is grafted (into SOFTWARE\Classes) is left to later work.
func buildHives(ctx context.Context, backing source.Backing, l *layout.Layout, acts []action.Action, outDir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hives, err := hive.BaseHives(backing)
	if err != nil {
		return nil, fmt.Errorf("build base hives: %w", err)
	}
	if err := hives.Apply(acts); err != nil {
		return nil, fmt.Errorf("apply registry actions: %w", err)
	}

	dir := configDir(l, outDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	files := []struct {
		name string
		h    *hive.Hive
	}{
		{"SYSTEM", hives.System},
		{"SOFTWARE", hives.Software},
		{"DEFAULT", hives.Default},
	}
	var written []string
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := hive.WriteHive(f.h)
		if err != nil {
			return nil, fmt.Errorf("serialize %s hive: %w", f.name, err)
		}
		full := filepath.Join(dir, f.name)
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s hive: %w", f.name, err)
		}
		rel, _ := filepath.Rel(outDir, full)
		written = append(written, filepath.ToSlash(rel))
	}
	return written, nil
}

// configDir resolves the absolute path of the tree's config folder under the
// system root, preferring the source's own directory-ID table.
func configDir(l *layout.Layout, outDir string) string {
	rel := configFallback
	if p, ok := l.DirPath(configDirID); ok && p != "" {
		rel = p
	}
	return filepath.Join(outDir, source.SystemRoot, filepath.FromSlash(rel))
}
