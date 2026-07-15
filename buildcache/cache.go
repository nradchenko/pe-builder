// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package buildcache is a small on-disk cache shared by build stages that
// recompute expensive but deterministic artifacts from an unchanging source —
// for example the members decompressed out of the multi-file driver cabinets,
// each of which costs a full LZX folder decode yet is identical across every
// rebuild of the same source. Entries are grouped by a stage namespace and
// addressed by a caller-supplied key of any length or contents (the store hashes
// it), so each stage owns its own key derivation and stages never collide.
//
// A nil *Cache is a valid, disabled cache: Get always misses and Put is a no-op.
// A stage therefore holds a *Cache unconditionally and never branches on whether
// caching is on. The cache is strictly an optimization — every miss and every
// write failure falls back to recomputation — so a missing, unwritable, or
// partially populated cache directory never fails a build.
package buildcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// Environment controls, honored by Open: envDisable turns caching off entirely;
// envDir overrides the cache root directory.
const (
	envDisable = "PEBUILD_NO_CACHE"
	envDir     = "PEBUILD_CACHE_DIR"
)

// Every entry file is an envelope: a magic tag, a digest of the payload, then the
// payload. Get verifies both before returning, so a truncated, bit-flipped, or
// foreign file (an unrelated file that happens to sit at an entry path, or an entry
// written by an incompatible format version) is treated as a miss and recomputed
// rather than served as a valid artifact. The filename is keyed by the source, not
// the content, so without this a corrupt entry would silently poison the output.
const (
	entryMagic  = "PBC1" // pe-builder cache, envelope v1
	entryHeader = len(entryMagic) + sha256.Size
)

// Cache is a keyed blob store rooted at a directory. A nil *Cache is a disabled
// cache, as is one whose directory is empty.
type Cache struct {
	dir string
}

// Open returns the default cache: disabled when PEBUILD_NO_CACHE is set;
// otherwise rooted at PEBUILD_CACHE_DIR when set, else at <user cache
// dir>/pe-builder. It yields a disabled cache (never an error) when no cache
// directory can be determined, so a build never fails for want of a cache.
func Open() *Cache {
	if os.Getenv(envDisable) != "" {
		return nil
	}
	dir := os.Getenv(envDir)
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(base, "pe-builder")
	}
	return &Cache{dir: dir}
}

// New returns a cache rooted at dir, or a disabled cache when dir is empty.
func New(dir string) *Cache {
	if dir == "" {
		return nil
	}
	return &Cache{dir: dir}
}

// Enabled reports whether the cache will actually store and serve entries.
func (c *Cache) Enabled() bool { return c != nil && c.dir != "" }

// Dir returns the cache's root directory ("" when disabled), for logging.
func (c *Cache) Dir() string {
	if c == nil {
		return ""
	}
	return c.dir
}

// path is the on-disk file backing (namespace, key). The key is hashed so it may
// be any length or contain any bytes; the namespace is a directory so a stage's
// entries are grouped and separately prunable.
func (c *Cache) path(namespace, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, namespace, hex.EncodeToString(sum[:]))
}

// Get returns the bytes stored under (namespace, key) and whether present. A
// disabled cache, a missing entry, a read error, or an entry that fails its
// integrity check (truncated, corrupted, or a foreign/incompatible file) all
// report a miss, so the caller recomputes rather than trusting bad bytes.
func (c *Cache) Get(namespace, key string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}
	raw, err := os.ReadFile(c.path(namespace, key))
	if err != nil {
		return nil, false
	}
	if len(raw) < entryHeader || string(raw[:len(entryMagic)]) != entryMagic {
		return nil, false
	}
	want := raw[len(entryMagic):entryHeader]
	payload := raw[entryHeader:]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(want, sum[:]) {
		return nil, false
	}
	return payload, true
}

// Put stores data under (namespace, key), replacing any existing entry. It writes
// to a temporary file and renames it into place, so a crash or a concurrent
// writer never leaves a torn entry a later Get could read. Every failure is
// swallowed: the cache is an optimization, not a source of build errors. A
// disabled cache is a no-op.
func (c *Cache) Put(namespace, key string, data []byte) {
	if !c.Enabled() {
		return
	}
	dir := filepath.Join(c.dir, namespace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return
	}
	tmp := f.Name()
	// Envelope: magic || sha256(payload) || payload. Written as header then payload
	// (two writes) so a large payload is not copied into a combined buffer first.
	header := make([]byte, entryHeader)
	copy(header, entryMagic)
	sum := sha256.Sum256(data)
	copy(header[len(entryMagic):], sum[:])
	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, c.path(namespace, key)); err != nil {
		os.Remove(tmp)
	}
}
