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
// actions, and writes them where the /minint boot loads them. It returns the
// output-relative paths written, for the report.
//
// The SYSTEM hive is written to system32\SETUPREG.HIV, not system32\config\SYSTEM:
// in /minint (text-setup) mode the loader loads setupreg.hiv as the SYSTEM hive,
// while config\SYSTEM is the installed-Windows path the loader never consults.
// SOFTWARE and the default-user hive go in config, where the session/logon chain
// loads them. The CLASSES hive the engine also builds is not written — a bare boot
// to cmd.exe does not need HKCR.
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

	config := configDir(l, outDir)
	system32 := filepath.Dir(config)
	if err := os.MkdirAll(config, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	files := []struct {
		path string
		h    *hive.Hive
	}{
		{filepath.Join(system32, "SETUPREG.HIV"), hives.System},
		{filepath.Join(config, "SOFTWARE"), hives.Software},
		{filepath.Join(config, "DEFAULT"), hives.Default},
		// The config manager requires SAM and SECURITY to exist and be loadable at boot
		// or the boot bugchecks (0xc0000218). Structurally-valid empty hives (a bare
		// root) satisfy the load AND avoid the LSA create-race: shipping them zero-length
		// makes the LSA build the SECURITY policy DB from scratch while msprivs is
		// initializing, which loses the race intermittently (lsass 0xc0000142). A
		// pre-formed empty hive removes that create-race, so it is the reliable choice.
		{filepath.Join(config, "SAM"), hive.NewHive("SAM")},
		{filepath.Join(config, "SECURITY"), hive.NewHive("SECURITY")},
	}
	var written []string
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := hive.WriteHive(f.h)
		if err != nil {
			return nil, fmt.Errorf("serialize hive %s: %w", filepath.Base(f.path), err)
		}
		if err := os.WriteFile(f.path, data, 0o644); err != nil {
			return nil, fmt.Errorf("write hive %s: %w", filepath.Base(f.path), err)
		}
		rel, _ := filepath.Rel(outDir, f.path)
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
