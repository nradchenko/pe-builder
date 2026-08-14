// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import "io/fs"

// Normalized wraps fsys with the same case and separator normalization every source backing
// carries, so a filesystem from elsewhere — the tree inside a WIM, say — resolves paths exactly
// as a directory or an ISO backing does.
func Normalized(fsys fs.FS) Backing { return newNormFS(fsys) }
