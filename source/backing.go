// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"io/fs"
	"path"
	"strings"
	"sync"
)

// normFS wraps a Backing so lookups tolerate the case and separator differences
// between an ISO (case-sensitive, uppercase 8.3 names) and an extracted directory,
// and the mixed forms plugin .inf files use (i386\ntdll.dll vs I386/NTDLL.DLL).
//
// Only Open is overridden, and it MUST stay that way: fs.ReadDir/ReadFile/Stat find
// no ReadDirFS/ReadFileFS/StatFS on normFS and so fall back to Open plus the opened
// file's own ReadDirFile/ReadAll/Stat — inheriting the normalization. Adding those
// optional interfaces here (forwarding straight to fsys) would silently route those
// calls around the case-insensitive resolve. The common case — an exact-path hit —
// does no extra directory reads; the resolve walk runs only when the literal path
// is absent.
//
// dirs caches each directory's case-folding map so the resolve walk reads a given
// directory at most once. On an ISO whose names are uppercase 8.3 while the pipeline
// asks for lowercase, the literal Open misses on essentially every file, so resolve
// runs constantly; without the cache each run re-parses the whole (multi-thousand
// entry) directory. The cache is a shared pointer so it survives normFS being copied
// by value as a Backing, and it is guarded for the concurrent walks pewalk runs.
type normFS struct {
	fsys fs.FS
	dirs *dirCache
}

// newNormFS wraps fsys with case/separator normalization and a fresh directory
// cache. Every normFS must have a cache (resolve relies on it), so construct through
// this rather than a bare struct literal.
func newNormFS(fsys fs.FS) normFS {
	return normFS{fsys: fsys, dirs: newDirCache()}
}

// dirCache memoizes a directory's lowercased-name → real-name map, so a repeated
// case-insensitive resolve in the same directory is a map lookup rather than a fresh
// ReadDir and per-entry parse. It is safe for concurrent use.
//
// Source media cannot hold two entries differing only in case (an ISO stores a single
// uppercase name; a Windows source is case-insensitive), so a lowercase key maps to a
// unique real name — matching resolve's existing "first case-insensitive match wins".
type dirCache struct {
	mu      sync.Mutex
	byLower map[string]map[string]string // dir path → lowercase entry name → real name
}

func newDirCache() *dirCache {
	return &dirCache{byLower: map[string]map[string]string{}}
}

// lookup returns the real-case name of the entry in dir matching name (exact first,
// then case-insensitive), reading and caching dir's listing on first use. The bool is
// false when dir has no such entry; err is non-nil only when dir itself cannot be read.
func (c *dirCache) lookup(fsys fs.FS, dir, name string) (string, bool, error) {
	c.mu.Lock()
	m, ok := c.byLower[dir]
	c.mu.Unlock()
	if !ok {
		// Read outside the lock: ReadDir is the expensive call this cache exists to
		// avoid, and holding the lock would serialize concurrent walks of other dirs.
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return "", false, err
		}
		m = make(map[string]string, len(entries))
		for _, e := range entries {
			m[strings.ToLower(e.Name())] = e.Name()
		}
		c.mu.Lock()
		if existing, raced := c.byLower[dir]; raced {
			m = existing // a concurrent reader already stored an equivalent map.
		} else {
			c.byLower[dir] = m
		}
		c.mu.Unlock()
	}
	real, found := m[strings.ToLower(name)]
	return real, found, nil
}

// normalize converts backslashes to forward slashes and reduces the path to the
// unrooted, cleaned form io/fs expects ("." for the root).
func normalize(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimLeft(name, "/")
	if name == "" {
		return "."
	}
	return path.Clean(name)
}

// Open resolves name (after normalization) exactly, then case-insensitively.
//
// If the literal path fails, a case-insensitive resolve is attempted; when that
// finds a real entry under a different casing, it is opened. Otherwise the original
// error is returned unchanged — so a genuine I/O or corruption error surfaces
// rather than being masked as a missing file. (This avoids depending on any
// backing's not-found error wording.)
func (n normFS) Open(name string) (fs.File, error) {
	p := normalize(name)
	f, err := n.fsys.Open(p)
	if err == nil {
		return f, nil
	}
	if resolved, rerr := n.resolve(p); rerr == nil && resolved != p {
		if rf, rerr := n.fsys.Open(resolved); rerr == nil {
			return rf, nil
		}
	}
	return nil, err
}

// resolve walks p one component at a time, matching each against the parent
// directory's entries: exact first, then EqualFold. It returns the real cased
// path, or an error if any component has no case-insensitive match. Each
// directory's listing is read once and cached (see dirCache), so resolving many
// files in the same directory does not re-parse it per file.
func (n normFS) resolve(p string) (string, error) {
	if p == "." {
		return ".", nil
	}
	cur := "."
	for _, part := range strings.Split(p, "/") {
		match, found, err := n.dirs.lookup(n.fsys, cur, part)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fs.ErrNotExist
		}
		if cur == "." {
			cur = match
		} else {
			cur += "/" + match
		}
	}
	return cur, nil
}
