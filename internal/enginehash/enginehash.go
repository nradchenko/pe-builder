// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package enginehash computes a content hash over the file trees pebuild embeds —
// the base layer and the built-in plugins — so a build can record which engine
// produced it.
//
// It deliberately hashes content rather than the pebuild executable. The binary
// carries a build timestamp stamped in at link time, so every rebuild produces
// different bytes even with no source change; a hash of it would report "changed"
// after every no-op rebuild. The embedded trees are what is semantically baked into
// the binary, and they are stable across a rebuild that changed nothing.
package enginehash

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
)

// Sum returns the combined content hash of the given file trees as "sha256:<hex>".
//
// The walk is deterministic: fs.WalkDir visits entries in lexical order, and each
// file contributes its path and its content — both length-prefixed, so no combination
// of names and bytes can collide with a different one — to a single digest. Hashing
// the path as well as the content means a rename alone changes the result. Trees are
// folded in the order given, so callers must pass them in a fixed order.
//
// Directories contribute nothing: an empty directory cannot survive go:embed anyway,
// so hashing it would make the result depend on how the tree was obtained.
func Sum(fss ...fs.FS) (string, error) {
	h := sha256.New()
	var buf [8]byte
	write := func(b []byte) {
		binary.BigEndian.PutUint64(buf[:], uint64(len(b)))
		h.Write(buf[:])
		h.Write(b)
	}
	for _, fsys := range fss {
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				return fmt.Errorf("read %s: %w", p, err)
			}
			write([]byte(p))
			write(data)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("hash embedded tree: %w", err)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// SumBytes returns "sha256:<hex>" over a byte slice. It is the same notation as Sum,
// for the inputs that are single files rather than trees (a build manifest).
func SumBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
