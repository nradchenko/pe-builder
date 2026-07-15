// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package tree assembles the output file tree from a build's action list. It
// executes the file-placement actions — copy (with on-the-fly decompression) and
// mkdir — and ignores the registry, boot, and text-patch actions, which later
// stages (hive building, boot configuration) consume. A custom directory can be
// overlaid verbatim afterwards.
//
// Two output conventions are fixed here:
//
//   - System root = i386. The tree mirrors a Windows CD: system-root-relative
//     directories (system32, system32\drivers, …) live under an i386 directory at
//     the output root, so a file bound for directory ID 2 lands at
//     <out>/i386/system32/<name>. Directories flagged "from root" in
//     [WinntDirectories] are created at the output root instead.
//   - Casing is preserved. Files keep their canonical (source) case; the uppercase
//     rename Windows requires for CD booting (the attribute-8 flag) is applied when
//     the ISO is mastered, not here, so the on-disk tree stays in one consistent
//     case for golden diffing.
package tree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/buildcache"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// systemRoot is the output directory the source's system-root-relative tree is
// placed under, mirroring a Windows installation CD's I386 folder. It is also the
// source folder files are read from. The value is defined once, in package source,
// and shared by the source reader, this assembler, and the hive stage.
const systemRoot = source.SystemRoot

// Result records what an assembly produced, for reporting and golden listings.
type Result struct {
	// Written holds the output-relative (forward-slash) paths of files written.
	Written []string
	// Skipped holds the canonical names of optional files absent from the source.
	Skipped []string
}

// Assemble executes the file-placement actions in acts into a tree under outRoot,
// resolving each file through the source layout and reading its bytes from src.
// Actions apply in order; a later write to the same path wins. Missing optional
// files are skipped and recorded; other read/write failures are accumulated and
// returned together so one bad file does not abort the build.
func Assemble(ctx context.Context, acts []action.Action, l *layout.Layout, src source.Backing, origins map[string]fs.FS, outRoot string, cache *buildcache.Cache) (*Result, error) {
	res := &Result{}
	var errs []error

	// The driver cabinets are consulted only for copy targets absent loosely; collect
	// every copy name up front so the cabinet layer can extract them in a single pass
	// and release each (large) archive immediately. Files a plugin ships itself (an
	// Origin is set) come from the plugin, not the Windows source, so they are left
	// out of the cabinet search.
	want := make(map[string]bool)
	for _, a := range acts {
		// A [SourcePaths] copy (SrcPath set) reads an exact path, not a cabinet member,
		// so it is left out of the cabinet search.
		if a.Kind == action.CopyFile && a.Origin == "" && a.SrcPath == "" {
			want[strings.ToLower(a.Src)] = true
		}
	}
	cabs := newCabinets(src, want, cache)

	for _, a := range acts {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		switch a.Kind {
		case action.CopyFile:
			written, skipped, err := placeFile(a, l, src, cabs, origins, outRoot)
			switch {
			case err != nil:
				errs = append(errs, err)
			case skipped != "":
				res.Skipped = append(res.Skipped, skipped)
			default:
				res.Written = append(res.Written, written)
			}
		case action.Mkdir:
			if err := makeDir(a, l, outRoot); err != nil {
				errs = append(errs, err)
			}
		default:
			// Registry, boot, and text-patch actions are executed by later stages.
		}
	}

	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	return res, errors.Join(errs...)
}

// makeDir creates the directory named by a Mkdir action, honoring its "from root"
// placement. AlwaysCreate is implicit — the directory is created whether or not any
// file will land in it.
func makeDir(a action.Action, l *layout.Layout, outRoot string) error {
	rel := dirPath(a.DirID, a.Path, l)
	root := filepath.Join(outRoot, systemRoot)
	if a.FromRoot {
		root = outRoot
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(full, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", rel, err)
	}
	return nil
}

// Overlay copies the contents of customDir verbatim into outRoot, overwriting any
// existing files (last-writer-wins), and returns the output-relative paths written.
// It is the "custom directory" feature: extra files added to the build untouched.
// A customDir of "" is a no-op.
func Overlay(ctx context.Context, outRoot, customDir string) ([]string, error) {
	if customDir == "" {
		return nil, nil
	}
	var written []string
	err := filepath.WalkDir(customDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(customDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(outRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(written)
	return written, err
}
