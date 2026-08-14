// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"

	wim "github.com/nradchenko/go-wim"
	"github.com/nradchenko/pe-builder/source"
)

// wimLayoutBacking returns a backing that reads through a wim-layout image's PE.wim, presenting
// the media root and the WIM's payload as one namespace: the boot chain stays listable and
// i386/... resolves to the payload inside the WIM, so every command downstream is unchanged.
//
// The WIM is read on demand through go-wim's Open(io.ReaderAt): only the chunks a listing or an
// extract touches are decoded, and the compressed image is never held resident in full. PE.wim is
// most of the ISO, so slurping it (OpenBytes) would cost the whole image's size in RAM to read one
// small file. The backing's file stays open for the reader's lifetime, which for a one-shot CLI is
// the process's.
func wimLayoutBacking(b source.Backing) (source.Backing, error) {
	f, err := b.Open(wimImageFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", wimImageFile, err)
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", wimImageFile, err)
	}
	ra, streams, err := wimReaderAt(f)
	if err != nil {
		return nil, err
	}
	rd, err := wim.Open(ra, fi.Size())
	if err != nil {
		return nil, err
	}
	im, err := rd.Boot()
	if err != nil {
		return nil, err
	}
	keep = streams // hold the file open only when the reader streams from it
	return mergedFS{media: b, payload: source.Normalized(im.FS())}, nil
}

// wimReaderAt adapts a backing's file into a concurrency-safe io.ReaderAt for go-wim (its reader
// requires one). A file that already is a ReaderAt — a directory backing's *os.File — is used
// directly; the go-diskfs ISO file is a ReadSeeker, so it is wrapped; a backing that offers
// neither is read into memory as a last resort. streams reports whether the returned ReaderAt
// keeps reading from f (so the caller must keep f open) rather than from an in-memory copy.
func wimReaderAt(f fs.File) (ra io.ReaderAt, streams bool, err error) {
	if r, ok := f.(io.ReaderAt); ok {
		return r, true, nil
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		return &seekReaderAt{rs: rs}, true, nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	return bytes.NewReader(data), false, nil
}

// seekReaderAt adapts an io.ReadSeeker into a concurrency-safe io.ReaderAt by serialising
// seek-then-read under a mutex. go-wim's reader may issue ReadAt concurrently and requires the
// ReaderAt be safe for it; a bare ReadSeeker is not, because the offset is shared state.
type seekReaderAt struct {
	mu sync.Mutex
	rs io.ReadSeeker
}

func (s *seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.rs.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(s.rs, p)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF // ReaderAt reports a short read at end-of-file as io.EOF
	}
	return n, err
}

// wimPayloadOpaque reports whether b is a wim-layout image whose payload could not be read
// through — the reader failed or is unavailable, so only the media root is visible. When the
// payload IS readable, openImage/readHiveData return a mergedFS and this is false, so the
// inspection commands drop the "extract PE.wim" guidance and behave as they do for a classic
// image. It is what turns the guardrail into a fallback rather than an always-on warning.
func wimPayloadOpaque(b source.Backing) bool {
	if _, ok := b.(mergedFS); ok {
		return false
	}
	return isWIMLayoutImage(b)
}

// mergedFS presents the payload over the media root. The payload wins a name held by both, which
// in practice is only the build receipt: it is written into the tree and copied to the media root
// so verify can read it from outside.
type mergedFS struct{ media, payload source.Backing }

// Open tries the payload first, then the media root. It falls back to the media only when the
// payload genuinely lacks the name (fs.ErrNotExist): a real read error — a corrupt or
// undecodable payload entry — is surfaced rather than masked as a media miss, which would
// otherwise read as "no file in the image".
func (m mergedFS) Open(name string) (fs.File, error) {
	f, err := m.payload.Open(name)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return m.media.Open(name)
}

func (m mergedFS) Stat(name string) (fs.FileInfo, error) {
	fi, err := fs.Stat(m.payload, name)
	if err == nil {
		return fi, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return fs.Stat(m.media, name)
}

// ReadDir merges the two listings, the payload entry winning a name held by both (keyed
// case-insensitively, since the ISO stage may recase). This assumes no name is a directory on one
// side and a file on the other: were that to happen, the winner's kind would decide whether
// WalkDir descends, and the loser's subtree could be hidden. It holds for the wim layout — the
// media root carries only files (the loader, the boot core, the WIM, the receipt), and the sole
// payload/media overlap is the receipt, a file on both sides.
func (m mergedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	seen := map[string]fs.DirEntry{}
	a, errA := fs.ReadDir(m.payload, name)
	b, errB := fs.ReadDir(m.media, name)
	if errA != nil && errB != nil {
		return nil, errA
	}
	for _, e := range b {
		seen[strings.ToLower(e.Name())] = e
	}
	for _, e := range a {
		seen[strings.ToLower(e.Name())] = e
	}
	out := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}
