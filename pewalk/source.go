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
	"io"
	"io/fs"
	"path"
	"sort"
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
	// cabFP maps a cabinet's path to a fingerprint of its directory region,
	// computed once at index time. It keys the extracted-member cache so a member
	// decompressed on an earlier build of the same source is reused rather than
	// re-decoded (a full LZX folder decode) here.
	cabFP map[string]string
	// cache is the shared on-disk store of extracted cabinet members. It is nil
	// (disabled) unless the caller passes WithCache, and every use is guarded on that:
	// unlike a concrete *buildcache.Cache, whose methods are nil-receiver-safe, an
	// unset interface would panic rather than simply miss.
	cache Cache
	// warnings records non-fatal indexing problems (e.g. an unreadable cabinet
	// whose members were skipped), for the caller to surface.
	warnings []string

	mu      sync.Mutex
	cabined map[string]*compress.Cabinet // cabinet path -> parsed framing (lazy)
	// parsed caches a module's resolution and parse by canonical name, so a module
	// is read, decompressed, and PE-parsed at most once per Source even when many
	// overlapping closure walks (the base layer plus each [Closure] plugin) traverse
	// the same shared subgraph. Populated lazily on the first walk to touch a module.
	parsed map[string]*parsedModule
}

// parsedModule is the cached, walk-mode-independent result of resolving a module
// once: its resolution status and origin, and — once its imports have been parsed —
// its import tables split into static and delay-load name sets (each sorted and
// de-duplicated). Imports are depth-independent, so one cache entry serves walks with
// any depth cap or import mode.
//
// parsed distinguishes a resolved module whose PE has been parsed for imports from
// one that was only located (its status and origin are known, but the import parse
// was skipped because the module was first reached at a depth cap and its imports
// were not needed). A later walk that does need its imports re-reads and parses it.
type parsedModule struct {
	status     Status
	origin     Origin
	parsed     bool
	static     []string
	delay      []string
	parseError string
}

// imports returns the module's import names for a walk mode: static only, or the
// static-plus-delay union. Each call returns a fresh slice the caller owns. Only valid
// once the module has been parsed (m.parsed).
func (m *parsedModule) imports(includeDelay bool) []string {
	return mergeImports(m.static, m.delay, includeDelay)
}

// Option configures a Source at Open time.
type Option func(*Source)

// Cache is the on-disk store of extracted cabinet members a caller may attach with
// WithCache. It is named as an interface rather than as the engine's concrete cache type
// so that pewalk — the package most likely to be used on its own — keeps its exported
// signatures to types a caller can name.
type Cache interface {
	// Get returns the bytes stored under (namespace, key), and whether one was present.
	Get(namespace, key string) ([]byte, bool)
	// Put stores data under (namespace, key), replacing any existing entry.
	Put(namespace, key string, data []byte)
}

// WithCache attaches a shared on-disk cache of extracted cabinet members, so a
// member decompressed on an earlier build of the same source is reused instead of
// decoded again. Omitting the option, or passing an untyped nil, leaves caching off; the
// standalone CLI passes none.
//
// Note what a typed nil does, since Cache is an interface: a nil pointer of an
// implementing type is a non-nil Cache, so it is attached and its methods are called on a
// nil receiver. An implementation that can be handed to WithCache that way must tolerate
// one, as the engine's own cache does.
func WithCache(c Cache) Option {
	return func(s *Source) { s.cache = c }
}

// Open indexes fsys and returns a Source. It walks the tree once, recording every
// file by name and enumerating the members of every *.cab cabinet from just the
// cabinet's directory — its header and file table — without reading the far larger
// compressed data, which is read lazily on first extraction. Compressed-loose files
// are likewise resolved (and decoded) lazily by name. So indexing a source touches
// only kilobytes of each multi-megabyte cabinet, and a build whose file set is all
// loose never reads a cabinet's data at all.
func Open(fsys fs.FS, opts ...Option) (*Source, error) {
	s := &Source{
		fsys:    fsys,
		files:   map[string]string{},
		cab:     map[string]string{},
		cabFP:   map[string]string{},
		cabined: map[string]*compress.Cabinet{},
		parsed:  map[string]*parsedModule{},
	}
	for _, o := range opts {
		o(s)
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		// First writer wins over a lexical walk, so which copy of a repeated name this
		// picks is at least deterministic. It is not known to be the right one: two loose
		// files sharing a name are not always the same file. On XP Home shfolder.dll is
		// both VALUEADD\MSFT\USMT\ANSI\SHFOLDER.DLL (md5 7c8f371c…) and I386\SHFOLDER.DL_
		// (md5 c14aa058…) — same size, different bytes — and this index answers with the
		// first while a build stages the second, since it reads only the area root.
		//
		// That is the same index-versus-stage drift indexCabinet was given a precedence
		// rule to fix, still open on the loose side. It is left alone here rather than
		// guessed at: the ordering a build actually wants is the one tree.readSource
		// applies, and lifting that into this package is a change with its own reasoning
		// to do, not a line to slip into a cabinet fix.
		//
		// The practical consequence today is that `source ls` and `source extract` can
		// name a different copy than a build stages.
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

// cabProbeSize is the initial bounded read used to locate a cabinet's directory: it
// only needs to span the CFHEADER and CFFOLDER table (a handful of bytes per folder),
// so 64 KiB comfortably covers any real cabinet's header before the directory's exact
// length is known.
//
// The CFFILE table that follows may run past this probe (DRIVER.CAB's is ~122 KiB);
// readCabinetDir handles that with a continuation read. Only the CFHEADER + CFFOLDER
// table must fit within the probe, since CabinetDirSize needs the whole folder table
// to compute the directory length. A cabinet whose folder table alone exceeds 64 KiB
// — thousands of folders, not seen on real install media — cannot be sized: it is
// skipped with a warning and its members resolve as Missing, the same safe
// degradation as any unreadable cabinet, never a crash. Widen the probe (or make
// CabinetDirSize signal "need more") if such a cabinet ever turns up.
const cabProbeSize = 64 << 10

// indexCabinet records each member name of the cabinet at p without reading its data
// payloads: it reads just the directory region (header, folders, file entries) and
// parses that. The cabinet's compressed data is read lazily by cabinet() on the first
// extraction, so a source whose file set never resolves to a cabinet member pays only
// the small directory read here.
func (s *Source) indexCabinet(p string) error {
	dir, err := s.readCabinetDir(p)
	if err != nil {
		return err
	}
	// ReadCabinet over just the directory enumerates members; Data is never called on
	// this cabinet, so the absent payloads do not matter. Extraction goes through
	// cabinet(), which reads the whole file on demand.
	c, err := compress.ReadCabinet(dir)
	if err != nil {
		return err
	}
	// Fingerprint the directory region now, while it is in hand, so member
	// extraction can key the cache without re-reading the cabinet.
	s.cabFP[p] = compress.Fingerprint(dir)
	for _, f := range c.Files() {
		name := strings.ToLower(f.Name)
		// Precedence, not discovery order. A medium may carry the same member in more
		// than one cabinet — a slipstreamed source keeps both SP2.CAB and SP3.CAB — and
		// the newest service pack is the copy the system would use. Taking whichever the
		// walk reached first means taking the oldest, since the walk is lexical.
		if cur, ok := s.cab[name]; ok && cabRank(cur) <= cabRank(p) {
			continue
		}
		s.cab[name] = p
	}
	return nil
}

// cabPrecedence lists the source's multi-file cabinets in the order a member should be
// taken from them: a service-pack cabinet supersedes the base DRIVER.CAB, and a later
// service pack supersedes an earlier one. A cabinet not named here ranks below all of
// them, and between two equally ranked cabinets the first one walked wins, so the choice
// stays deterministic either way.
//
// ASMS01.CAB holds the side-by-side assemblies and shares no member with the others, so
// its position only decides anything if that ever changes.
//
// This is the whole of the policy, and both halves of the engine read it here: the file
// set is computed from whichever copy this names, and the same copy has to be the one
// staged, or a build ships a binary whose dependencies were resolved against a different
// one — which is a missing-DLL failure at boot, reported against the wrong file.
var cabPrecedence = []string{"sp3.cab", "sp2.cab", "sp1.cab", "driver.cab", "asms01.cab"}

// CabinetOrder returns the cabinet names in precedence order, highest first. Callers that
// open the cabinets themselves iterate this so they read a member from the same one the
// index resolved it to.
func CabinetOrder() []string { return append([]string(nil), cabPrecedence...) }

// cabRank orders a cabinet path by precedence: lower is stronger. An unlisted cabinet
// ranks last, below every named one.
func cabRank(cabPath string) int {
	base := strings.ToLower(path.Base(cabPath))
	for i, n := range cabPrecedence {
		if base == n {
			return i
		}
	}
	return len(cabPrecedence)
}

// cabCacheKey returns the extracted-member cache key for name in the cabinet at
// cabPath, or "" when the cabinet has no fingerprint (so the member is not
// cacheable and extraction must decode it).
func (s *Source) cabCacheKey(cabPath, name string) string {
	fp := s.cabFP[cabPath]
	if fp == "" {
		return ""
	}
	return fp + ":" + strings.ToLower(name)
}

// readCabinetDir returns the directory region (header, folders, file entries; up to
// the first CFDATA block) of the cabinet at p. It opens the file once: it reads a
// probe covering the header, learns the directory's exact length, and — only if the
// directory runs past the probe — continues reading from the same handle. Opening a
// file resolves a path (not free on an ISO backing), so this pays that cost once
// while still reading only kilobytes of a multi-megabyte cabinet.
func (s *Source) readCabinetDir(p string) ([]byte, error) {
	f, err := s.fsys.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, cabProbeSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	buf = buf[:n]

	dirLen, err := compress.CabinetDirSize(buf)
	if err != nil {
		return nil, err
	}
	if dirLen > len(buf) {
		// Continue from where the probe left off — the handle is positioned right
		// after it — rather than reopening the file.
		rest := make([]byte, dirLen-len(buf))
		m, err := io.ReadFull(f, rest)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		buf = append(buf, rest[:m]...)
	}
	if len(buf) < dirLen {
		return nil, fmt.Errorf("truncated cabinet directory (%d of %d bytes)", len(buf), dirLen)
	}
	return buf[:dirLen], nil
}

// Has reports whether name resolves to a module in the source.
func (s *Source) Has(name string) bool {
	_, ok := s.Locate(name)
	return ok
}

// Locate resolves name to its origin without reading or decompressing anything,
// following the same loose → compressed-loose → cabinet-member precedence as Read.
// It answers "is this file on the media, and in what form" for a whole file list at
// index cost, where Read would decode each one.
func (s *Source) Locate(name string) (Origin, bool) {
	key := strings.ToLower(name)
	if p, ok := s.files[key]; ok {
		return Origin{Kind: OriginLoose, Path: p}, true
	}
	if ck := compressedForm(key); ck != key {
		if p, ok := s.files[ck]; ok {
			return Origin{Kind: OriginCompressedLoose, Path: p}, true
		}
	}
	if cabPath, ok := s.cab[key]; ok {
		return Origin{Kind: OriginCab, Path: cabPath}, true
	}
	return Origin{}, false
}

// IndexEntry is one file the source index holds: the name it is stored under — the
// on-disk file name for a loose or compressed-loose file (shell32.dll, shell32.dl_),
// the member name for a cabinet member — and where that file lives.
type IndexEntry struct {
	Name   string
	Origin Origin
}

// Index returns every file the source indexed, sorted by name. Names are as stored,
// so a compressed-loose entry keeps its *.??_ spelling: the canonical name is not
// recoverable from it (dl_ could be dll or dlx), and only the source's own metadata
// or the archive's directory knows which. Nothing is read or decompressed.
func (s *Source) Index() []IndexEntry {
	out := make([]IndexEntry, 0, len(s.files)+len(s.cab))
	for name, p := range s.files {
		kind := OriginLoose
		if strings.HasSuffix(name, "_") {
			kind = OriginCompressedLoose
		}
		out = append(out, IndexEntry{Name: name, Origin: Origin{Kind: kind, Path: p}})
	}
	for name, cabPath := range s.cab {
		out = append(out, IndexEntry{Name: name, Origin: Origin{Kind: OriginCab, Path: cabPath}})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Origin.Path < out[j].Origin.Path
	})
	return out
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

// resolveModule reads and (when needImports is set) PE-parses a module once, caching
// the result on the Source so overlapping closure walks reuse it instead of repeating
// the work. A module absent from the source is cached as a Missing entry, so a red
// herring is not re-attempted per walk. It is the read path WalkWith uses in place of
// a bare Read plus parse.
//
// needImports lets the caller skip the import parse for a module it will not expand —
// a leaf at the walk's depth cap needs only status and origin. A cached but unparsed
// entry is upgraded (re-read and parsed) if a later walk does need its imports; the
// common unbounded walk always needs them and parses on first touch.
func (s *Source) resolveModule(name string, needImports bool) *parsedModule {
	key := moduleName(name)

	s.mu.Lock()
	if m, ok := s.parsed[key]; ok && cacheServes(m, needImports) {
		s.mu.Unlock()
		return m
	}
	s.mu.Unlock()

	// Do the I/O and parse without holding the lock: Read may itself parse a cabinet
	// (which takes the same mutex), and the decompress/parse is expensive enough that
	// serializing it would defeat any concurrent walk.
	m := &parsedModule{}
	data, origin, err := s.Read(name)
	if err != nil {
		m.status = StatusMissing
	} else {
		m.status = StatusResolved
		m.origin = origin
		if needImports {
			m.parsed = true
			if static, delay, perr := importTables(data); perr != nil {
				m.parseError = perr.Error()
			} else {
				m.static, m.delay = static, delay
			}
		}
	}

	s.mu.Lock()
	// A concurrent resolveModule may have populated the entry while we worked. Keep it
	// if it already serves this request; otherwise our entry is at least as complete
	// (we parsed, it did not), so it wins. Either way an entry is never downgraded.
	if existing, ok := s.parsed[key]; ok && cacheServes(existing, needImports) {
		m = existing
	} else {
		s.parsed[key] = m
	}
	s.mu.Unlock()
	return m
}

// cacheServes reports whether a cached entry satisfies a resolveModule request: a
// missing module is final, and a resolved one serves unless imports are needed but
// were not parsed.
func cacheServes(m *parsedModule, needImports bool) bool {
	return m.status == StatusMissing || m.parsed || !needImports
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

// readCabMember extracts name from the cabinet at cabPath. On a cache hit it
// returns the stored member bytes without opening or decoding the cabinet — the
// point of the cache, since extraction otherwise decodes the whole (up to
// hundreds of megabytes) LZX folder the member lives in. On a miss it decodes via
// the framing parsed at index time and stores the result for the next build.
func (s *Source) readCabMember(cabPath, name string) ([]byte, Origin, error) {
	origin := Origin{Kind: OriginCab, Path: cabPath}
	key := s.cabCacheKey(cabPath, name)
	if key != "" && s.cache != nil {
		if data, ok := s.cache.Get(compress.CabinetCacheNamespace, key); ok {
			return data, origin, nil
		}
	}
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
	if key != "" && s.cache != nil {
		s.cache.Put(compress.CabinetCacheNamespace, key, data)
	}
	return data, origin, nil
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
