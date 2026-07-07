// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package tree

import (
	"io/fs"
	"log/slog"
	"path"
	"strings"
	"sync"

	"github.com/nradchenko/pe-builder/compress"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// cabNames are the source's multi-file driver cabinets, in precedence order:
// a service-pack cabinet supersedes the base DRIVER.CAB, so it is consulted
// first. A file present loosely in the source still wins over both (see
// readSource) — the loose copy is the slipstreamed, current one.
var cabNames = []string{"sp3.cab", "sp2.cab", "sp1.cab", "driver.cab"}

// cabinets extracts the members named in a wanted set from the source's driver
// cabinets, for files (many class and HID drivers) that ship only inside
// DRIVER.CAB / SP*.CAB rather than as a loose *.??_ archive.
//
// The cabinets are large (DRIVER.CAB alone is tens of megabytes) and
// compress.Cabinet retains the whole archive to decode folders on demand, so
// this keeps none of them resident: on first use it opens each cabinet in turn,
// copies out the wanted members, and lets the archive be freed before opening
// the next. Only the extracted member bytes (a handful of small drivers) survive.
// A malformed or truncated cabinet is skipped with a warning rather than failing
// the build, so the loose tree still assembles.
type cabinets struct {
	src  source.Backing
	want map[string]bool // lowercased member names to extract
	once sync.Once
	got  map[string][]byte // lowercased name -> final (decompressed) bytes
}

// newCabinets returns a resolver that will extract the named members (lowercased)
// on first lookup. Extraction is deferred, so a build needing no cabinet-only
// file never opens a cabinet.
func newCabinets(src source.Backing, want map[string]bool) *cabinets {
	return &cabinets{src: src, want: want}
}

// load opens each present cabinet once, extracts the wanted members that are not
// available loosely and not already found (earlier cabinets take precedence), and
// releases the archive before the next. Absent cabinets are skipped; a malformed
// one is skipped with a warning.
//
// Skipping members that exist loosely matters: the wanted set is every copy
// target, most of which are on the media as loose files. readSource only reaches
// the cabinets for genuine misses, so extracting a loose-present driver here would
// decompress a cabinet folder whose result is never used.
func (c *cabinets) load() {
	c.got = make(map[string][]byte)
	loose := c.looseNames()
	for _, name := range cabNames {
		data, err := fs.ReadFile(c.src, path.Join(source.SystemRoot, name))
		if err != nil {
			continue // this cabinet is not on the media
		}
		cab, err := compress.ReadCabinet(data)
		if err != nil {
			slog.Warn("skipping malformed source cabinet", "cabinet", name, "err", err)
			continue
		}
		for _, f := range cab.Files() {
			ln := strings.ToLower(f.Name)
			if !c.want[ln] {
				continue
			}
			if _, done := c.got[ln]; done {
				continue // an earlier (higher-precedence) cabinet had it
			}
			if loose[ln] || loose[strings.ToLower(layout.CompressedName(ln))] {
				continue // resolvable loosely; the cabinet copy would go unused
			}
			b, err := cab.Data(f)
			if err != nil {
				slog.Warn("skipping undecodable cabinet member", "cabinet", name, "file", f.Name, "err", err)
				continue
			}
			// Copy out of the archive's cached folder so the whole cabinet can be
			// freed once this loop iteration ends.
			member := make([]byte, len(b))
			copy(member, b)
			c.got[ln] = member
		}
		// cab (and its decoded-folder cache and the archive bytes) becomes
		// unreachable here and is collected before the next, larger cabinet opens.
	}
}

// looseNames returns the set of file names (lowercased) present directly under the
// source's system-root directory, read once so the extraction loop can cheaply
// skip members that are already available as loose files.
func (c *cabinets) looseNames() map[string]bool {
	set := map[string]bool{}
	entries, err := fs.ReadDir(c.src, source.SystemRoot)
	if err != nil {
		return set
	}
	for _, e := range entries {
		set[strings.ToLower(e.Name())] = true
	}
	return set
}

// Find returns the extracted bytes of the named member, and whether it was found.
// The bytes are the file's final content (the cabinet's compression is already
// undone), so the caller does not decompress again.
func (c *cabinets) Find(name string) ([]byte, bool) {
	c.once.Do(c.load)
	b, ok := c.got[strings.ToLower(name)]
	return b, ok
}
