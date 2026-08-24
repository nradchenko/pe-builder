// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package receipt

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// ErrNotFound reports that an image carries no receipt at all. It is deliberately its
// own outcome, distinct from a receipt that fails to match: an image built before
// receipts existed, or not built by pebuild, is a different answer from one built by a
// different pebuild, and reporting the first as the second would be a lie.
var ErrNotFound = errors.New("no build receipt")

// ReadFS reads the receipt from the root of a built tree or a mastered image. It accepts
// either spelling of the name: the tree writes it lowercase, and the media stage
// uppercases the whole tree on the way to the disc.
func ReadFS(fsys fs.FS) (*Receipt, error) {
	for _, name := range []string{FileName, strings.ToUpper(FileName)} {
		data, err := fs.ReadFile(fsys, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue // not under this spelling; try the other
		}
		if err != nil {
			// A receipt that is there but unreadable — a permission error, a truncated
			// image, an I/O failure — is not an absent one. Falling through would report
			// the image as predating receipts, which asserts something about how it was
			// built on the strength of a failure to read it.
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		r, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return r, nil
	}
	return nil, ErrNotFound
}
