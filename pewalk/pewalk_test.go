// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/compress"
	"github.com/nradchenko/pe-builder/internal/buildcache"
	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/source"
)

// cabMemberKey is the extracted-member cache key pewalk derives for name in cab —
// a fingerprint of the cabinet's directory region plus the member name.
func cabMemberKey(t *testing.T, cab []byte, name string) string {
	t.Helper()
	n, err := compress.CabinetDirSize(cab)
	if err != nil {
		t.Fatalf("CabinetDirSize: %v", err)
	}
	return compress.Fingerprint(cab[:n]) + ":" + name
}

// The engine's cache, pinned at compile time as one this package accepts. It is
// belt-and-braces rather than the only guard — build passes a *buildcache.Cache straight
// into WithCache, so a drift in its method set already fails that build. This states the
// constraint beside the interface it belongs to, where it survives that caller changing.
var _ Cache = (*buildcache.Cache)(nil)

// TestCabMemberReadWithoutCache covers the Source no caller attached a cache to — the
// standalone CLI's case. Cache is an interface, so an unattached cache is a nil interface
// and an unguarded call would panic here rather than simply miss, which is how the
// concrete cache behaved.
func TestCabMemberReadWithoutCache(t *testing.T) {
	want := bytes.Repeat([]byte{0xCC}, 400)
	cab := writeCabinet([]cabMember{{name: "cabmod.dll", data: want}})

	s, err := Open(fstest.MapFS{"i386/drivers.cab": {Data: cab}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Read("cabmod.dll")
	if err != nil {
		t.Fatalf("read with no cache attached: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cabmod.dll: got %d bytes, want %d", len(got), len(want))
	}
}

// TestCabMemberReadWithNilCache covers WithCache handed an explicit nil. It is a separate
// case from the one above: this leaves a non-nil interface holding a nil pointer, which a
// plain s.cache != nil guard does not catch, so the methods behind it must stay
// nil-receiver-safe.
func TestCabMemberReadWithNilCache(t *testing.T) {
	want := bytes.Repeat([]byte{0xDD}, 400)
	cab := writeCabinet([]cabMember{{name: "cabmod.dll", data: want}})

	s, err := Open(fstest.MapFS{"i386/drivers.cab": {Data: cab}}, WithCache((*buildcache.Cache)(nil)))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Read("cabmod.dll")
	if err != nil {
		t.Fatalf("read with a nil cache: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cabmod.dll: got %d bytes, want %d", len(got), len(want))
	}
}

// TestCabMemberCacheHitAndInvalidation checks that extraction serves a cached
// member on a hit, and — the correctness-critical property — never serves a stale
// member once the source cabinet changes.
func TestCabMemberCacheHitAndInvalidation(t *testing.T) {
	cabA := writeCabinet([]cabMember{{name: "cabmod.dll", data: bytes.Repeat([]byte{0xAA}, 300)}})
	cache := buildcache.New(t.TempDir())

	// Seed a sentinel under cabA's exact key: a hit must short-circuit extraction
	// and return it, which proves the key derivation matches between store and read.
	cache.Put(compress.CabinetCacheNamespace, cabMemberKey(t, cabA, "cabmod.dll"), []byte("SENTINEL"))
	sA, err := Open(fstest.MapFS{"i386/drivers.cab": {Data: cabA}}, WithCache(cache))
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := sA.Read("cabmod.dll"); err != nil {
		t.Fatal(err)
	} else if string(got) != "SENTINEL" {
		t.Fatalf("cache hit not served: got %d bytes, want the seeded sentinel", len(got))
	}

	// A different cabinet (member resized and rewritten) fingerprints differently,
	// so the stale sentinel is not served — the real member is decoded instead.
	realB := bytes.Repeat([]byte{0xBB}, 512)
	cabB := writeCabinet([]cabMember{{name: "cabmod.dll", data: realB}})
	sB, err := Open(fstest.MapFS{"i386/drivers.cab": {Data: cabB}}, WithCache(cache))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := sB.Read("cabmod.dll")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "SENTINEL" {
		t.Fatal("stale cache entry served after the source cabinet changed")
	}
	if !bytes.Equal(got, realB) {
		t.Fatalf("cabmod.dll: got %d bytes, want the new member content", len(got))
	}

	// The miss populated the cache under cabB's key, so the next build reuses it.
	if cached, ok := cache.Get(compress.CabinetCacheNamespace, cabMemberKey(t, cabB, "cabmod.dll")); !ok || !bytes.Equal(cached, realB) {
		t.Fatalf("miss did not populate cache under the new key: ok=%v", ok)
	}
}

// fixtureSource assembles a small closure that exercises every resolution path:
//
//	app.exe (loose)            → gui.dll, leaf.dll, absent.dll, delaymod.dll (delay)
//	gui.dll (compressed loose) → leaf.dll, cabmod.dll
//	leaf.dll (loose)           → (none)      [shared: a diamond via app + gui]
//	cabmod.dll (cabinet)       → (none)
//	delaymod.dll (loose)       → (none)
//	absent.dll                 → missing (a red herring)
func fixtureSource(t *testing.T) *Source {
	t.Helper()
	fsys := fstest.MapFS{
		"i386/app.exe": {Data: buildPE(
			[]string{"gui.dll", "leaf.dll", "absent.dll"},
			[]string{"delaymod.dll"}, false)},
		"i386/leaf.dll":     {Data: buildPE(nil, nil, true)},
		"i386/delaymod.dll": {Data: buildPE(nil, nil, true)},
		"i386/gui.dl_": {Data: writeCabinet([]cabMember{
			{name: "gui.dll", data: buildPE([]string{"leaf.dll", "cabmod.dll"}, nil, true)},
		})},
		"i386/drivers.cab": {Data: writeCabinet([]cabMember{
			{name: "cabmod.dll", data: buildPE(nil, nil, true)},
		})},
	}
	s, err := Open(fsys)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestSourceResolution(t *testing.T) {
	s := fixtureSource(t)
	cases := []struct {
		name string
		kind OriginKind
		path string
	}{
		{"app.exe", OriginLoose, "i386/app.exe"},
		{"APP.EXE", OriginLoose, "i386/app.exe"}, // case-insensitive
		{"gui.dll", OriginCompressedLoose, "i386/gui.dl_"},
		{"cabmod.dll", OriginCab, "i386/drivers.cab"},
	}
	for _, c := range cases {
		if !s.Has(c.name) {
			t.Errorf("Has(%q) = false, want true", c.name)
		}
		_, origin, err := s.Read(c.name)
		if err != nil {
			t.Errorf("Read(%q): %v", c.name, err)
			continue
		}
		if origin.Kind != c.kind || origin.Path != c.path {
			t.Errorf("Read(%q) origin = %v %q, want kind %v path %q",
				c.name, origin.Kind, origin.Path, c.kind, c.path)
		}
	}

	if s.Has("absent.dll") {
		t.Errorf("Has(absent.dll) = true, want false")
	}
	if _, _, err := s.Read("absent.dll"); err == nil {
		t.Errorf("Read(absent.dll) = nil error, want not-found")
	}
}

func TestWalkClosure(t *testing.T) {
	s := fixtureSource(t)
	res, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	wantOrder := []string{"app.exe", "absent.dll", "delaymod.dll", "gui.dll", "leaf.dll", "cabmod.dll"}
	if len(res.Order) != len(wantOrder) {
		t.Fatalf("Order = %v, want %v", res.Order, wantOrder)
	}
	for i, name := range wantOrder {
		if res.Order[i] != name {
			t.Fatalf("Order = %v, want %v", res.Order, wantOrder)
		}
	}

	if got := res.Nodes["absent.dll"].Status; got != StatusMissing {
		t.Errorf("absent.dll status = %v, want missing", got)
	}
	if got := res.Nodes["cabmod.dll"].Origin.Kind; got != OriginCab {
		t.Errorf("cabmod.dll origin = %v, want cabinet", got)
	}
	// leaf.dll is imported by both app.exe and gui.dll but appears once (map-keyed),
	// and is a normal dependency, not a seed.
	if node, ok := res.Nodes["leaf.dll"]; !ok {
		t.Error("leaf.dll missing from nodes")
	} else if node.Seed {
		t.Error("leaf.dll = seed, want non-seed")
	}
	if got := len(res.Missing()); got != 1 {
		t.Errorf("Missing count = %d, want 1", got)
	}
}

func TestWalkOutputGolden(t *testing.T) {
	s := fixtureSource(t)
	res, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	var text bytes.Buffer
	res.WriteText(&text, false)
	golden.Assert(t, "walk/text", text.String())

	var missing bytes.Buffer
	res.WriteText(&missing, true)
	golden.Assert(t, "walk/text-missing-only", missing.String())

	var dot bytes.Buffer
	res.WriteDOT(&dot)
	golden.Assert(t, "walk/dot", dot.String())

	var jsonBuf bytes.Buffer
	if err := res.WriteJSON(&jsonBuf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	golden.Assert(t, "walk/json", jsonBuf.String())
}

func TestWalkMaxDepth(t *testing.T) {
	s := fixtureSource(t)
	res, err := WalkWith(s, Options{MaxDepth: 1}, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	// Depth 1 reaches app.exe's direct imports but does not expand gui.dll, so
	// cabmod.dll (only reachable through gui.dll) is never discovered.
	if _, ok := res.Nodes["cabmod.dll"]; ok {
		t.Errorf("cabmod.dll discovered at max-depth 1, want it bounded out")
	}
	if _, ok := res.Nodes["gui.dll"]; !ok {
		t.Errorf("gui.dll not discovered at max-depth 1")
	}
	if imports := res.Nodes["gui.dll"].Imports; imports != nil {
		t.Errorf("gui.dll imports = %v, want none expanded at depth cap", imports)
	}
}

func TestMissingSeedIsNotFatal(t *testing.T) {
	s := fixtureSource(t)
	res, err := Walk(s, "nosuch.exe")
	if err != nil {
		t.Fatalf("Walk with missing seed: %v", err)
	}
	if got := res.Nodes["nosuch.exe"].Status; got != StatusMissing {
		t.Errorf("missing seed status = %v, want missing", got)
	}
}

// TestWalkDirectoryBacking exercises the same path the CLI uses: a real on-disk
// directory opened through the source package (os.DirFS behind the case-folding
// normFS wrapper), rather than an in-memory MapFS. It confirms compressed-loose
// and cabinet resolution work over the real backing and that lookups tolerate the
// case differences that backing normalizes.
func TestWalkDirectoryBacking(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "i386")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"app.exe":     buildPE([]string{"gui.dll", "absent.dll"}, nil, false),
		"gui.dl_":     writeCabinet([]cabMember{{name: "gui.dll", data: buildPE([]string{"cabmod.dll"}, nil, true)}}),
		"drivers.cab": writeCabinet([]cabMember{{name: "cabmod.dll", data: buildPE(nil, nil, true)}}),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	src, err := source.Open(dir)
	if err != nil {
		t.Fatalf("source.Open: %v", err)
	}
	s, err := Open(src.Backing())
	if err != nil {
		t.Fatalf("pewalk.Open: %v", err)
	}
	// Query with uppercase to exercise case-insensitive resolution end to end.
	res, err := Walk(s, "APP.EXE")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	if got := res.Nodes["gui.dll"].Origin.Kind; got != OriginCompressedLoose {
		t.Errorf("gui.dll origin = %v, want compressed-loose", got)
	}
	if got := res.Nodes["cabmod.dll"].Origin.Kind; got != OriginCab {
		t.Errorf("cabmod.dll origin = %v, want cabinet", got)
	}
	if got := res.Nodes["absent.dll"].Status; got != StatusMissing {
		t.Errorf("absent.dll status = %v, want missing", got)
	}
}

// TestImportTablesMalformedDoesNotPanic feeds importTables inputs that are not
// valid PE images (including ones that have tripped saferwall panics) and asserts it
// returns an error instead of panicking — so one bad module never aborts a walk.
func TestImportTablesMalformedDoesNotPanic(t *testing.T) {
	cases := map[string][]byte{
		"empty":        {},
		"tiny":         []byte("MZ"),
		"not-pe":       []byte("this is plainly not a PE binary at all, just text"),
		"mz-no-pe":     append([]byte("MZ"), make([]byte, 200)...),
		"truncated-pe": buildPE([]string{"leaf.dll"}, nil, false)[:80],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			static, delay, err := importTables(data)
			if err == nil {
				t.Errorf("importTables(%s) = %v/%v, nil; want an error", name, static, delay)
			}
		})
	}
}

// TestIndexLargeCabinetDirectory covers directory-only indexing when a cabinet's
// directory is larger than the probe read: the members past the probe must still be
// enumerated, exercising readCabinetDir's single-Open continuation.
func TestIndexLargeCabinetDirectory(t *testing.T) {
	// ~3000 members put the CFFILE table well past the 64 KiB probe (each entry is
	// ~29 bytes), so the last member is only reachable via the continuation read.
	const n = 3000
	members := make([]cabMember, n)
	for i := range members {
		members[i] = cabMember{name: fmt.Sprintf("mod%05d.dll", i), data: []byte{0}}
	}
	fsys := fstest.MapFS{
		"i386/big.cab": {Data: writeCabinet(members)},
	}
	s, err := Open(fsys)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(s.Warnings()) != 0 {
		t.Fatalf("unexpected indexing warnings: %v", s.Warnings())
	}
	// The first and last members bracket the directory; both being indexed proves the
	// whole directory — well beyond the probe — was read from the single Open.
	for _, name := range []string{"mod00000.dll", fmt.Sprintf("mod%05d.dll", n-1)} {
		if !s.Has(name) {
			t.Errorf("Has(%q) = false, want true (directory past the probe not indexed)", name)
		}
	}
}

// TestUnreadableCabinetIsSkipped confirms a cabinet the compress package cannot
// parse is skipped with a warning rather than aborting Open, and the rest of the
// source still resolves.
func TestUnreadableCabinetIsSkipped(t *testing.T) {
	fsys := fstest.MapFS{
		"i386/app.exe":  {Data: buildPE([]string{"leaf.dll"}, nil, false)},
		"i386/leaf.dll": {Data: buildPE(nil, nil, true)},
		// "MSCF" makes IsCabinet true, but the truncated header fails ReadCabinet.
		"i386/broken.cab": {Data: []byte("MSCF\x00\x00\x00\x00")},
	}
	s, err := Open(fsys)
	if err != nil {
		t.Fatalf("Open must not fail on a bad cabinet: %v", err)
	}
	if len(s.Warnings()) != 1 {
		t.Fatalf("Warnings = %v, want exactly one skipped-cabinet warning", s.Warnings())
	}
	res, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := res.Nodes["leaf.dll"].Status; got != StatusResolved {
		t.Errorf("leaf.dll status = %v, want resolved despite the bad cabinet", got)
	}
}

// TestMalformedModuleYieldsCleanOutput checks that a resolvable-but-unparseable
// module becomes a ParseError node (not a crash) and that JSON output stays valid
// — i.e. no stray parser diagnostics leak onto stdout.
func TestMalformedModuleYieldsCleanOutput(t *testing.T) {
	fsys := fstest.MapFS{
		"i386/app.exe":  {Data: buildPE([]string{"junk.dll"}, nil, false)},
		"i386/junk.dll": {Data: []byte("MZ not really a PE, will fail to parse")},
	}
	s, err := Open(fsys)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	res, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	junk := res.Nodes["junk.dll"]
	if junk.Status != StatusResolved || junk.ParseError == "" {
		t.Errorf("junk.dll = status %v parseError %q; want resolved with a parse error",
			junk.Status, junk.ParseError)
	}

	var buf bytes.Buffer
	if err := res.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var decoded jsonResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, buf.String())
	}
}

// countingFS wraps an fs.FS and counts how many times each path is opened. Embedding
// the FS as the bare fs.FS interface (not the concrete MapFS) hides MapFS's
// ReadFileFS/ReadDirFS fast paths, so every fs.ReadFile the Source performs is forced
// through Open and thus counted.
type countingFS struct {
	fs.FS
	mu    sync.Mutex
	opens map[string]int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.mu.Lock()
	c.opens[name]++
	c.mu.Unlock()
	return c.FS.Open(name)
}

func (c *countingFS) reset() {
	c.mu.Lock()
	c.opens = map[string]int{}
	c.mu.Unlock()
}

func (c *countingFS) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens[name]
}

// TestWalkCachesModuleReads confirms the per-Source parse cache collapses the read
// amplification that overlapping closure walks would otherwise incur: with the base
// layer plus every [Closure] plugin each running its own walk over a shared subgraph,
// a module must still be read and decompressed from the backing at most once per
// build, not once per walk.
func TestWalkCachesModuleReads(t *testing.T) {
	base := fstest.MapFS{
		"i386/app.exe": {Data: buildPE([]string{"gui.dll", "leaf.dll"}, nil, false)},
		"i386/gui.dl_": {Data: writeCabinet([]cabMember{
			{name: "gui.dll", data: buildPE([]string{"leaf.dll"}, nil, true)},
		})},
		"i386/leaf.dll": {Data: buildPE(nil, nil, true)},
	}
	cfs := &countingFS{FS: base, opens: map[string]int{}}
	s, err := Open(cfs)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Isolate the walks' reads from the one-time indexing reads Open performs.
	cfs.reset()

	// Three overlapping walks, as a build runs: the base closure plus several
	// [Closure] plugins, all seeded on the same shared modules.
	for i := 0; i < 3; i++ {
		res, err := WalkWith(s, Options{StaticOnly: true}, "app.exe", "gui.dll")
		if err != nil {
			t.Fatalf("walk %d: %v", i, err)
		}
		if got := res.Nodes["leaf.dll"].Status; got != StatusResolved {
			t.Fatalf("walk %d: leaf.dll status = %v, want resolved", i, got)
		}
	}

	// Each module's backing file is opened exactly once across all three walks; the
	// cache serves the rest. Without it, each walk re-reads, giving three opens apiece.
	for _, p := range []string{"i386/app.exe", "i386/gui.dl_", "i386/leaf.dll"} {
		if got := cfs.count(p); got != 1 {
			t.Errorf("opens[%s] = %d across three walks, want 1 (cache should absorb the rest)", p, got)
		}
	}
}

// TestNodeImportsNotAliasedAcrossWalks guards that a node's Imports slice is the
// caller's own copy, never a reference into the shared parse cache: mutating one
// walk's result must not affect a later walk over the same (cached) Source.
func TestNodeImportsNotAliasedAcrossWalks(t *testing.T) {
	s := fixtureSource(t)
	res1, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	imps1 := res1.Nodes["gui.dll"].Imports
	if len(imps1) == 0 {
		t.Fatalf("expected gui.dll to have imports")
	}
	orig := imps1[0]
	imps1[0] = "MUTATED.dll" // scribble on the first walk's slice

	res2, err := Walk(s, "app.exe")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := res2.Nodes["gui.dll"].Imports[0]; got != orig {
		t.Errorf("second walk gui.dll imports[0] = %q, want %q — the cache was aliased by the first walk's slice", got, orig)
	}
}

// TestWalkDepthCapDefersParse confirms a module first reached at the depth cap is
// located but not parsed (its imports are not recorded), and that a later unbounded
// walk over the same Source upgrades the deferred entry — re-reading and parsing it —
// so modules only reachable through it are still discovered.
func TestWalkDepthCapDefersParse(t *testing.T) {
	s := fixtureSource(t)

	// Depth 1 reaches gui.dll but does not expand it: its imports stay unparsed, so
	// cabmod.dll (only reachable through gui.dll) is not discovered.
	capped, err := WalkWith(s, Options{MaxDepth: 1}, "app.exe")
	if err != nil {
		t.Fatalf("capped walk: %v", err)
	}
	if _, ok := capped.Nodes["cabmod.dll"]; ok {
		t.Errorf("cabmod.dll discovered at depth 1, want it bounded out")
	}
	if imports := capped.Nodes["gui.dll"].Imports; imports != nil {
		t.Errorf("gui.dll imports = %v at the cap, want none recorded", imports)
	}

	// An unbounded walk over the same Source must upgrade gui.dll's deferred entry and
	// discover cabmod.dll through its now-parsed imports.
	full, err := WalkWith(s, Options{}, "app.exe")
	if err != nil {
		t.Fatalf("full walk: %v", err)
	}
	cab, ok := full.Nodes["cabmod.dll"]
	if !ok {
		t.Fatalf("cabmod.dll not discovered after the unbounded walk — deferred parse was not upgraded")
	}
	if cab.Status != StatusResolved {
		t.Errorf("cabmod.dll status = %v, want resolved", cab.Status)
	}
	if full.Nodes["gui.dll"].Imports == nil {
		t.Errorf("gui.dll imports still nil after the unbounded walk — deferred entry not re-parsed")
	}
}

// Guard: the fixture MapFS resolves through fs.WalkDir the same way a real source
// backing does.
var _ fs.FS = fstest.MapFS(nil)
