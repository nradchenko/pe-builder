// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"fmt"
	"io/fs"

	diskfs "github.com/diskfs/go-diskfs"
)

// openISO backs a source with an .iso file. go-diskfs reads the plain ISO9660
// image (no Joliet) that XP/2003 install media use, and its FileSystem already
// implements io/fs.FS with io/fs path conventions; normFS adds the case and
// separator tolerance the rest of the pipeline relies on. The disk handle stays
// open, read-only, for the lifetime of the backing.
func openISO(path string) (Backing, error) {
	disk, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return nil, fmt.Errorf("open iso %q: %w", path, err)
	}
	filesystem, err := disk.GetFilesystem(0)
	if err != nil {
		return nil, fmt.Errorf("read iso filesystem %q: %w", path, err)
	}
	fsys, ok := filesystem.(fs.FS)
	if !ok {
		return nil, fmt.Errorf("iso %q: filesystem does not support random-access reads", path)
	}
	return normFS{fsys: fsys}, nil
}
