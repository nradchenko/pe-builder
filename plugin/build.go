// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Built is the result of building a build-capable plugin: the directory the build
// wrote to and the set of file names it produced (lowercased). A [SourceDisksFiles]
// entry naming a produced file resolves from Dir rather than the Windows source.
type Built struct {
	Dir      fs.FS
	Produces map[string]bool
}

// buildOrigin is the file-origin key under which a built plugin's output directory is
// registered, distinct from the plugin's own shipped-files origin (its name).
func buildOrigin(plugin string) string { return "build:" + plugin }

// RunBuild compiles a build-capable plugin's source and captures its output. It
// returns (nil, nil) for a plugin with no [Build] section. Steps, in order:
//
//  1. every tool in Requires must be on PATH (a clear error names the plugin and tool);
//  2. the plugin's source is materialized into a fresh directory under workRoot — never
//     built in place, since an embedded or vendored plugin directory is read-only;
//  3. Command (default "make") runs there;
//  4. every file in Produces must exist afterward, and becomes a plugin-built origin.
//
// The trust decision — whether a plugin is allowed to run its command at all — is the
// caller's; RunBuild assumes it has been made.
func RunBuild(ctx context.Context, d *Discovered, workRoot string) (*Built, error) {
	spec := d.Build()
	if !spec.Present {
		return nil, nil
	}

	if d.FS == nil {
		return nil, fmt.Errorf("plugin %q: no source directory to build", d.Name)
	}

	for _, tool := range spec.Requires {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("plugin %q needs %q on PATH to build: %w", d.Name, tool, err)
		}
	}

	buildDir, err := os.MkdirTemp(workRoot, "build-*")
	if err != nil {
		return nil, err
	}
	if err := os.CopyFS(buildDir, d.FS); err != nil {
		return nil, fmt.Errorf("plugin %q: materialize source: %w", d.Name, err)
	}

	command := spec.Command
	if command == "" {
		command = "make"
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = buildDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("plugin %q: build command %q failed: %w\n%s", d.Name, command, err, out)
	}

	produces := make(map[string]bool, len(spec.Produces))
	for _, name := range spec.Produces {
		if !produced(buildDir, name) {
			return nil, fmt.Errorf("plugin %q: build did not produce %q", d.Name, name)
		}
		produces[strings.ToLower(name)] = true
	}
	return &Built{Dir: os.DirFS(buildDir), Produces: produces}, nil
}

// produced reports whether the build wrote name into dir, matching the case-tolerance
// of the tree's origin read (exact name, then its lowercased form) so a Produces entry
// whose case differs from the emitted file is not a spurious failure.
func produced(dir, name string) bool {
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
		return true
	}
	if lower := strings.ToLower(name); lower != name {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(lower))); err == nil {
			return true
		}
	}
	return false
}
