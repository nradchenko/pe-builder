// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Listing and resolving files is the same job whatever holds them, so it lives here
// rather than in any one command's file. A built PE image and a scratch disk are both
// read as a plain fs.FS — source.Backing is an alias for that interface, and the FAT
// volume a disk hands out satisfies it — so a single implementation of walking,
// matching and resolving serves both, and the two commands cannot drift apart in what
// a pattern means or how an ambiguous name is reported.
//
// What differs between them is only what a message calls the thing and what it can
// usefully add when nothing was found; that is what listing carries.

// listing is what the shared file helpers need to know about the container they are
// reading, which is only ever how to talk about it.
type listing struct {
	// noun names the container in a message — "image", "disk". It is the word a user
	// reads in "no file %q in the ...", so it should be the same word the command is
	// called by.
	noun string
	// hint, when set, is added to a listing that matched nothing and to a name that did
	// not resolve. It exists for the case where an empty-looking answer is misleading
	// rather than wrong — a WIM-layout image whose payload is sealed inside PE.wim looks
	// like a nearly empty image, and saying so is the difference between an answer and a
	// confusing one. A container with nothing to add leaves it empty.
	hint string
}

// walkFiles collects every file path in a filesystem, in sorted order.
func walkFiles(b fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(b, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// normalizePattern validates a caller's pattern once and returns the form matchPath
// expects: separators unified and lowercased. An empty pattern means match everything.
//
// Validating up front is what makes a typo distinguishable from an empty container: a
// malformed glob (`foo[`) would otherwise be swallowed by path.Match's error and
// reported as "0 file(s) matched", which reads as a confident answer.
func normalizePattern(pattern string) (string, error) {
	if pattern == "" {
		return "", nil
	}
	pat := strings.ToLower(strings.ReplaceAll(pattern, `\`, "/"))
	if _, err := path.Match(pat, "probe"); err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return pat, nil
}

// matchPath reports whether a path satisfies a normalized pattern, which may name the
// base name or the whole path, as a literal or a glob. Matching is case-insensitive
// because the container's own case is not stable: a build's tree writes mixed case and
// the ISO stage uppercases, and FAT has no stable case of its own either, so requiring
// the caller to know which one they have would make the same command work on one and
// fail on the other.
//
// The pattern arrives already normalized (see normalizePattern) so it is not
// re-lowercased for every path in the container.
func matchPath(p, pat string) bool {
	if pat == "" {
		return true
	}
	lp := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	if ok, _ := path.Match(pat, lp); ok {
		return true
	}
	if ok, _ := path.Match(pat, base); ok {
		return true
	}
	return lp == pat || base == pat
}

// printFileList writes the files matching a pattern, and a count.
func printFileList(w io.Writer, b fs.FS, pattern string, long bool, l listing) error {
	pat, err := normalizePattern(pattern)
	if err != nil {
		return err
	}
	paths, err := walkFiles(b)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	n := 0
	for _, p := range paths {
		if !matchPath(p, pat) {
			continue
		}
		n++
		if !long {
			fmt.Fprintln(tw, p)
			continue
		}
		// A size that cannot be read is reported as unknown rather than as a number:
		// printing -1 would read as data.
		size := "?"
		if fi, err := fs.Stat(b, p); err == nil {
			size = strconv.FormatInt(fi.Size(), 10)
		}
		fmt.Fprintf(tw, "%s\t%s\n", p, size)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%d file(s) matched.\n", n)
	// Add the hint when the caller listed everything or matched nothing — the two cases
	// where an empty-looking result would otherwise read as "that is all there is".
	if (pat == "" || n == 0) && l.hint != "" {
		fmt.Fprintf(w, "\nNote: %s.\n", l.hint)
	}
	return nil
}

// resolveName resolves a name to exactly one path, against a file list the caller has
// already walked. A full path is tried first, exactly and then ignoring case; failing that
// the name is matched as a bare name across the container. An ambiguous bare name is an
// error naming the candidates rather than a silent pick, since choosing one would quietly
// answer a different question than the one asked.
func resolveName(b fs.FS, paths []string, name string, l listing) (string, error) {
	p := strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
	if fi, err := fs.Stat(b, p); err == nil && !fi.IsDir() {
		return p, nil
	}

	// The same path in another case. fs.Stat cannot answer this on a FAT volume: it compares
	// directory entry names exactly, while the volume itself folds case when opening (see
	// internal/disk.Image.FS). A guest writes uppercase 8.3 short names, so a caller typing
	// the path a listing printed — which matches case-insensitively — would otherwise fall
	// through to the base-name pass and be told the name is ambiguous, advice they have
	// already taken. Matched against the walked list, which holds files only, so this cannot
	// resolve a directory that the exact test above just refused.
	for _, c := range paths {
		if strings.EqualFold(c, p) {
			return c, nil
		}
	}

	var hits []string
	for _, c := range paths {
		if strings.EqualFold(path.Base(c), path.Base(p)) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 0:
		if l.hint != "" {
			return "", fmt.Errorf("no file %q in the %s; %s", name, l.noun, l.hint)
		}
		return "", fmt.Errorf("no file %q in the %s", name, l.noun)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q is ambiguous in this %s (%s); name the exact path",
			name, l.noun, strings.Join(hits, ", "))
	}
}
