// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cloneTree mirrors the tree at src into a fresh temporary directory and returns it.
// Files are hard-linked where possible so a full second copy of a ~100 MB tree is
// not written; the ISO master's later removal of the clone drops only the extra
// directory entries, leaving src's files intact. The clone is created beside src (in
// its parent directory) so it lands on the same filesystem and the hard links
// succeed; a cross-device link still falls back to a byte copy.
func cloneTree(src string) (string, error) {
	dst, err := os.MkdirTemp(filepath.Dir(src), "pebuild-iso-*")
	if err != nil {
		return "", fmt.Errorf("create clone dir: %w", err)
	}
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Link(p, target); err == nil {
			return nil
		}
		return copyFile(p, target)
	})
	if err != nil {
		return "", fmt.Errorf("clone tree: %w", err)
	}
	return dst, nil
}

// copyFile streams the contents of src to a new file at dst, preserving src's mode
// so the clone matches the hard-link path (which shares the inode's mode). It
// streams rather than reading whole files because tree files can be large; a partial
// dst on a mid-copy error is removed so the clone never carries a truncated file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
