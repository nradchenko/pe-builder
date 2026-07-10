// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package pewalk computes the transitive PE import closure of a Windows binary
// against an installation source, resolving each imported module in the source
// and reporting what is present and what is missing. It is the "ldd for PE": a
// library plus a standalone tool (cmd/pewalk) that renders the closure as text,
// Graphviz DOT, or JSON.
//
// A Windows source stores a module under several disguises, and pewalk resolves
// a bare module name (case-insensitively) against all of them:
//
//   - loose            — i386\shell32.dll, read verbatim;
//   - compressed loose — i386\shell32.dl_ (the ? → _ convention), a single-file
//     MSCF cabinet decoded via the compress package;
//   - cabinet member   — inside DRIVER.CAB / SP3.CAB, decoded via compress.
//
// The closure is DLL-granular: pewalk records which modules a binary imports
// (static and delay-load), not which functions, so imports-by-ordinal and export
// forwarders do not affect it. A module absent from the source is reported as
// Missing (a flagged red herring), never fatal — a real boot dependency is always
// present on the media.
package pewalk

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/nradchenko/pe-builder/compress"
)

// OriginKind identifies how a resolved module is stored in the source.
type OriginKind int

const (
	// OriginNone is the zero value: the module was not found in the source.
	OriginNone OriginKind = iota
	// OriginLoose is a plain on-disk file (shell32.dll).
	OriginLoose
	// OriginCompressedLoose is a single-file compressed cabinet (shell32.dl_).
	OriginCompressedLoose
	// OriginCab is a member of a multi-file cabinet (DRIVER.CAB, SP3.CAB).
	OriginCab
)

// Origin records where a module was resolved from, for reporting and the graph.
// Path is the on-disk file for a loose or compressed-loose module, and the
// containing cabinet for a cabinet member.
type Origin struct {
	Kind OriginKind
	Path string
}

// String renders an origin as a short human-readable label.
func (o Origin) String() string {
	switch o.Kind {
	case OriginLoose:
		return o.Path
	case OriginCompressedLoose:
		return o.Path + " (compressed)"
	case OriginCab:
		return o.Path + " (cabinet)"
	default:
		return "not found"
	}
}

// Source is an indexed, resolvable set of PE files backed by an fs.FS (a
// directory or an ISO). It maps a bare module name to bytes, transparently
// decoding compressed-loose files and cabinet members. It is safe for concurrent
// use.
type Source struct {
	fsys fs.FS

	// files maps a lowercased on-disk file name to its path within fsys. It holds
	// both loose modules (shell32.dll) and compressed-loose ones (shell32.dl_);
	// the trailing-underscore name distinguishes them at read time.
	files map[string]string
	// cab maps a lowercased cabinet-member name to its containing cabinet's path.
	cab map[string]string
	// warnings records non-fatal indexing problems (e.g. an unreadable cabinet
	// whose members were skipped), for the caller to surface.
	warnings []string

	mu      sync.Mutex
	cabined map[string]*compress.Cabinet // cabinet path -> parsed framing (lazy)
}

// Open indexes fsys and returns a Source. It walks the tree once, recording every
// file by name and enumerating the members of every *.cab cabinet (whose framing
// is parsed, but not decoded, up front). Compressed-loose files are resolved
// lazily by name, so they are not decoded during indexing.
func Open(fsys fs.FS) (*Source, error) {
	s := &Source{
		fsys:    fsys,
		files:   map[string]string{},
		cab:     map[string]string{},
		cabined: map[string]*compress.Cabinet{},
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		// First writer wins, so a top-level copy shadows a nested duplicate
		// deterministically (walk order is lexical).
		if _, ok := s.files[name]; !ok {
			s.files[name] = p
		}
		if strings.HasSuffix(name, ".cab") {
			// A cabinet pebuild's compress package cannot parse (a spanned member,
			// an unsupported framing, or a stray file mis-named *.cab) must not
			// abort the whole source: skip it and record a warning. Modules found
			// only inside it then resolve as Missing, consistent with the walk's
			// never-fatal handling of an absent module.
			if err := s.indexCabinet(p); err != nil {
				s.warnings = append(s.warnings, fmt.Sprintf("skipped unreadable cabinet %s: %v", p, err))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Warnings returns the non-fatal problems encountered while indexing the source,
// in discovery order — currently, cabinets that could not be parsed and whose
// members were therefore left out of the index. It is empty for a clean source.
func (s *Source) Warnings() []string { return s.warnings }

// indexCabinet parses a cabinet's framing and records each member name, keeping
// the parsed cabinet for later extraction.
func (s *Source) indexCabinet(p string) error {
	data, err := fs.ReadFile(s.fsys, p)
	if err != nil {
		return err
	}
	c, err := compress.ReadCabinet(data)
	if err != nil {
		return err
	}
	s.cabined[p] = c
	for _, f := range c.Files() {
		name := strings.ToLower(f.Name)
		if _, ok := s.cab[name]; !ok {
			s.cab[name] = p
		}
	}
	return nil
}

// Has reports whether name resolves to a module in the source.
func (s *Source) Has(name string) bool {
	key := strings.ToLower(name)
	if _, ok := s.files[key]; ok {
		return true
	}
	if ck := compressedForm(key); ck != key {
		if _, ok := s.files[ck]; ok {
			return true
		}
	}
	_, ok := s.cab[key]
	return ok
}

// Read resolves name and returns its decompressed PE bytes and origin. Resolution
// prefers a loose file, then a compressed-loose file, then a cabinet member. It
// returns fs.ErrNotExist wrapped when name is absent from the source.
func (s *Source) Read(name string) ([]byte, Origin, error) {
	key := strings.ToLower(name)

	if p, ok := s.files[key]; ok {
		return s.readFile(p)
	}
	if ck := compressedForm(key); ck != key {
		if p, ok := s.files[ck]; ok {
			// p is the compressed form (ends in '_'), so readFile decompresses it.
			return s.readFile(p)
		}
	}
	if cabPath, ok := s.cab[key]; ok {
		return s.readCabMember(cabPath, name)
	}
	return nil, Origin{}, fmt.Errorf("resolve %q: %w", name, fs.ErrNotExist)
}

// readFile returns the bytes of a resolved on-disk file, decompressing it when
// its name is a compressed-loose form (trailing underscore).
func (s *Source) readFile(p string) ([]byte, Origin, error) {
	raw, err := fs.ReadFile(s.fsys, p)
	if err != nil {
		return nil, Origin{}, err
	}
	if strings.HasSuffix(strings.ToLower(p), "_") {
		dec, err := compress.DecompressBytes(raw)
		if err != nil {
			return nil, Origin{}, fmt.Errorf("decompress %s: %w", p, err)
		}
		return dec, Origin{Kind: OriginCompressedLoose, Path: p}, nil
	}
	return raw, Origin{Kind: OriginLoose, Path: p}, nil
}

// readCabMember extracts name from the cabinet at cabPath, reusing the framing
// parsed at index time.
func (s *Source) readCabMember(cabPath, name string) ([]byte, Origin, error) {
	c, err := s.cabinet(cabPath)
	if err != nil {
		return nil, Origin{}, err
	}
	f, ok := c.Find(name)
	if !ok {
		return nil, Origin{}, fmt.Errorf("cabinet %s has no member %q", cabPath, name)
	}
	data, err := c.Data(f)
	if err != nil {
		return nil, Origin{}, fmt.Errorf("extract %s from %s: %w", name, cabPath, err)
	}
	return data, Origin{Kind: OriginCab, Path: cabPath}, nil
}

// cabinet returns the parsed cabinet at p, parsing (and caching) it on first use.
// Cabinets discovered during indexing are already cached.
func (s *Source) cabinet(p string) (*compress.Cabinet, error) {
	s.mu.Lock()
	c, ok := s.cabined[p]
	s.mu.Unlock()
	if ok {
		return c, nil
	}
	data, err := fs.ReadFile(s.fsys, p)
	if err != nil {
		return nil, err
	}
	c, err = compress.ReadCabinet(data)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cabined[p] = c
	s.mu.Unlock()
	return c, nil
}

// compressedForm returns name with its final character replaced by an underscore,
// the Windows convention for a compressed-loose file (shell32.dll → shell32.dl_).
// It returns name unchanged when name is empty or already ends in an underscore.
func compressedForm(name string) string {
	if name == "" || strings.HasSuffix(name, "_") {
		return name
	}
	return name[:len(name)-1] + "_"
}

// moduleName reduces an import-table DLL name to the bare, lowercased file name
// used as the closure's canonical key. Import tables occasionally carry a path or
// mixed casing; DLL names are case-insensitive on Windows.
func moduleName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	return strings.ToLower(path.Base(name))
}
