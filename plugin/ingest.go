// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/compress"
)

// zipMagic is the local-file-header signature every zip archive begins with.
var zipMagic = []byte("PK\x03\x04")

// member is one extracted archive entry: a sanitized forward-slash relative path and
// its bytes.
type member struct {
	path string
	data []byte
}

// Add installs a plugin archive — a BartPE-native .cab or a .zip — into the plugins
// directory dest, as a plain plugin directory the search path can then resolve. It
// returns the installed plugin name (dest/<name>/ holds the files).
//
// The name is the archive's single top-level directory when it contains one (the
// archive already wraps the plugin in its folder), otherwise the archive's base name
// (loose files are placed under a folder of that name). Member paths are sanitized so
// a crafted archive cannot escape dest (no absolute or ..-climbing paths). An existing
// plugin of the same name is not overwritten unless force is set.
func Add(archive, dest string, force bool) (string, error) {
	data, err := os.ReadFile(archive)
	if err != nil {
		return "", err
	}

	members, err := extract(data)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", archive, err)
	}
	if len(members) == 0 {
		return "", fmt.Errorf("%s: archive is empty", archive)
	}

	name, strip := pluginLayout(members, archive)
	if name == "" {
		return "", fmt.Errorf("%s: cannot derive a plugin name from the archive", archive)
	}
	// A plugin is a directory with an .inf at its root; reject an archive that would
	// install to a directory the search path could never discover.
	if !hasRootINF(members, strip) {
		return "", fmt.Errorf("%s is not a plugin archive: no .inf at the plugin root", archive)
	}

	target := filepath.Join(dest, name)
	if _, err := os.Stat(target); err == nil && !force {
		return "", fmt.Errorf("plugin %q already installed at %s (use --force to overwrite)", name, target)
	}

	// Extract into a sibling temp directory and swap it into place, so a mid-extraction
	// failure never leaves a half-written plugin (and, with --force, never destroys the
	// existing one before the replacement is complete).
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dest, ".add-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // no-op once renamed; cleans the temp on any early return

	for _, m := range members {
		rel := strings.TrimPrefix(m.path, strip)
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, m.data, 0o644); err != nil {
			return "", err
		}
	}

	if force {
		if err := os.RemoveAll(target); err != nil {
			return "", fmt.Errorf("replace %s: %w", target, err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", fmt.Errorf("install %s: %w", target, err)
	}
	return name, nil
}

// hasRootINF reports whether the members place at least one .inf directly at the
// plugin's root (once the strip prefix is removed), i.e. whether the archive really
// describes a plugin directory.
func hasRootINF(members []member, strip string) bool {
	for _, m := range members {
		rel := strings.TrimPrefix(m.path, strip)
		if !strings.Contains(rel, "/") && isINF(rel) {
			return true
		}
	}
	return false
}

// extract reads a .cab or .zip archive into its members, rejecting any entry whose
// path escapes the archive root.
func extract(data []byte) ([]member, error) {
	switch {
	case compress.IsCabinet(data):
		return extractCab(data)
	case bytes.HasPrefix(data, zipMagic):
		return extractZip(data)
	default:
		return nil, fmt.Errorf("unsupported archive format (want a .cab or .zip)")
	}
}

func extractCab(data []byte) ([]member, error) {
	cab, err := compress.ReadCabinet(data)
	if err != nil {
		return nil, err
	}
	var out []member
	for _, f := range cab.Files() {
		rel, ok := sanitize(f.Name)
		if !ok {
			return nil, fmt.Errorf("unsafe path in cabinet: %q", f.Name)
		}
		b, err := cab.Data(f)
		if err != nil {
			return nil, err
		}
		out = append(out, member{path: rel, data: b})
	}
	return out, nil
}

func extractZip(data []byte) ([]member, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out []member
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, ok := sanitize(f.Name)
		if !ok {
			return nil, fmt.Errorf("unsafe path in zip: %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, member{path: rel, data: b})
	}
	return out, nil
}

// sanitize normalizes an archive member name to a clean forward-slash relative path
// and reports whether it is safe — not absolute and not climbing out of the archive
// root with "..".
func sanitize(name string) (string, bool) {
	p := strings.ReplaceAll(name, `\`, "/")
	// A ':' means a Windows drive ("C:\...") or an alternate data stream — neither is
	// a valid plugin file name, and on Linux path.IsAbs would not catch the drive form.
	if path.IsAbs(p) || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", false
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." || clean == "" {
		return "", false
	}
	return clean, true
}

// pluginLayout derives the installed plugin name and the prefix to strip from member
// paths to make them relative to the plugin root. When every member shares one
// top-level directory, that directory is the plugin (strip "<top>/"); otherwise the
// archive's base name becomes the plugin folder and loose members keep their paths
// (nothing to strip).
func pluginLayout(members []member, archive string) (name, strip string) {
	if top := commonTopDir(members); top != "" {
		return top, top + "/"
	}
	return strings.TrimSuffix(filepath.Base(archive), filepath.Ext(archive)), ""
}

// commonTopDir returns the single top-level directory shared by every member, or ""
// if the members are loose at the root or spread across several top directories.
func commonTopDir(members []member) string {
	top := ""
	for _, m := range members {
		i := strings.IndexByte(m.path, '/')
		if i < 0 {
			return "" // a file at the archive root — not wrapped in a folder
		}
		dir := m.path[:i]
		if top == "" {
			top = dir
		} else if dir != top {
			return "" // members span more than one top directory
		}
	}
	return top
}
