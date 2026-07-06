// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"io/fs"
	"path"
	"strings"
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
type normFS struct {
	fsys fs.FS
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
// path, or an error if any component has no case-insensitive match.
func (n normFS) resolve(p string) (string, error) {
	if p == "." {
		return ".", nil
	}
	cur := "."
	for _, part := range strings.Split(p, "/") {
		entries, err := fs.ReadDir(n.fsys, cur)
		if err != nil {
			return "", err
		}
		match := ""
		for _, e := range entries {
			if e.Name() == part {
				match = part
				break
			}
		}
		if match == "" {
			for _, e := range entries {
				if strings.EqualFold(e.Name(), part) {
					match = e.Name()
					break
				}
			}
		}
		if match == "" {
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
