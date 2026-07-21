// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/source"
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
//  4. every file in Produces must exist afterward AND, when it is a PE image, be built for
//     arch — see checkMachine — and becomes a plugin-built origin.
//
// arch is the architecture being built for; it is what the produced images are checked
// against. An empty value resolves to the default architecture.
//
// The trust decision — whether a plugin is allowed to run its command at all — is the
// caller's; RunBuild assumes it has been made.
func RunBuild(ctx context.Context, d *Discovered, workRoot, arch string) (*Built, error) {
	spec := d.Build()
	if !spec.Present {
		return nil, nil
	}
	// The plugin scopes its build by architecture but not for this one. Parsing let it
	// through so it stays discoverable and describable; building it is what cannot work.
	if spec.MissingArchVariant != "" {
		return nil, fmt.Errorf("plugin %q declares no [Build.%s] and so cannot be built for %s: "+
			"add that section naming the architecture's toolchain, or drop the scoping",
			d.Name, spec.MissingArchVariant, spec.MissingArchVariant)
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
		// One resolution of "which file did the build write", used both to require the
		// output and to check it: if the two ever disagreed, checkMachine would stat a
		// path that does not exist, read no PE header, and pass — a guard that had rotted
		// into a no-op while still appearing to run.
		path, ok := producedPath(buildDir, name)
		if !ok {
			return nil, fmt.Errorf("plugin %q: build did not produce %q", d.Name, name)
		}
		if err := checkMachine(path, name, source.ResolveArch(arch)); err != nil {
			return nil, fmt.Errorf("plugin %q: %w", d.Name, err)
		}
		produces[strings.ToLower(name)] = true
	}
	return &Built{Dir: os.DirFS(buildDir), Produces: produces}, nil
}

// peMachine is the architecture a PE image is built for, or 0 when the file is not a PE.
// Only the COFF header's Machine field is read: enough to tell an image's architecture,
// and it costs a few bytes rather than a full parse.
func peMachine(path string) uint16 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var mz [2]byte
	if _, err := io.ReadFull(f, mz[:]); err != nil || mz[0] != 'M' || mz[1] != 'Z' {
		return 0
	}
	var lfanew [4]byte
	if _, err := f.ReadAt(lfanew[:], 0x3c); err != nil {
		return 0
	}
	off := int64(binary.LittleEndian.Uint32(lfanew[:]))
	var sig [6]byte // "PE\0\0" then the Machine field
	if _, err := f.ReadAt(sig[:], off); err != nil {
		return 0
	}
	if sig[0] != 'P' || sig[1] != 'E' || sig[2] != 0 || sig[3] != 0 {
		return 0
	}
	return binary.LittleEndian.Uint16(sig[4:])
}

// machineFor maps a target architecture to the PE machine value its images must carry.
// It has to list every architecture source.ArchFolders reports, or the check below
// silently stops applying for the ones it misses — TestMachineForCoversEveryArch pins
// that, since the two lists are declared in different packages and nothing else ties
// them together.
var machineFor = map[string]uint16{"i386": 0x014c, "amd64": 0x8664}

// checkMachine fails a build whose output is not for the architecture being built.
//
// Nothing else catches this. `make` decides by timestamp, and no plugin Makefile makes its
// outputs depend on ARCH, so building a plugin for one architecture and then another in the
// same directory reports "Nothing to be done" and leaves the first architecture's binary in
// place — verified. Built-ins are materialized into a fresh directory each time and so are
// safe, but an on-disk plugin directory is copied wholesale, carrying along whatever a
// developer's earlier `make` left there. The staged binary would then simply not run, with
// nothing in the build output hinting why. Checking what was produced also catches the more
// basic case of a Makefile that ignores ARCH altogether.
//
// A non-PE output (a script, a data file) has no machine type and is left alone. path is
// the resolved output (see producedPath); name is only used to name it in the error.
func checkMachine(path, name, arch string) error {
	want, ok := machineFor[arch]
	if !ok {
		return nil // unknown target architecture: nothing to compare against
	}
	got := peMachine(path)
	if got == 0 || got == want {
		return nil
	}
	return fmt.Errorf("build produced %q for the wrong architecture: PE machine 0x%04x, "+
		"expected 0x%04x for %s (a stale output from an earlier build, or a Makefile that "+
		"ignores the architecture)", name, got, want, arch)
}

// producedPath returns the path the build wrote name to inside dir, and whether it wrote
// it at all, matching the case-tolerance of the tree's origin read (exact name, then its
// lowercased form) so a Produces entry whose case differs from the emitted file is not a
// spurious failure. It returns the path rather than a bool so that everything asking
// "which file did the build write" — the existence requirement and the architecture check —
// answers it the same way, and cannot drift apart.
func producedPath(dir, name string) (string, bool) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	if lower := strings.ToLower(name); lower != name {
		p = filepath.Join(dir, filepath.FromSlash(lower))
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}
