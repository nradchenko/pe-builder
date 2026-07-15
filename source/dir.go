// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"io/fs"
	"os"
)

// openDir backs a source with an extracted source directory.
func openDir(path string) (Backing, error) {
	// os.DirFS defers validation to first use; confirm the directory exists now so
	// Open fails at construction with a clear error rather than on first read.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "opendir", Path: path, Err: fs.ErrInvalid}
	}
	return newNormFS(os.DirFS(path)), nil
}
