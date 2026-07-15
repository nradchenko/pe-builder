// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package buildcache

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	c := New(t.TempDir())
	if !c.Enabled() {
		t.Fatal("New with a dir should be enabled")
	}
	if _, ok := c.Get("cab", "k"); ok {
		t.Fatal("Get on an empty cache should miss")
	}
	want := []byte("decompressed bytes")
	c.Put("cab", "k", want)
	got, ok := c.Get("cab", "k")
	if !ok {
		t.Fatal("Get after Put should hit")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, want)
	}
}

func TestNamespaceIsolation(t *testing.T) {
	c := New(t.TempDir())
	c.Put("a", "same-key", []byte("from-a"))
	c.Put("b", "same-key", []byte("from-b"))
	if got, _ := c.Get("a", "same-key"); string(got) != "from-a" {
		t.Errorf("namespace a: got %q", got)
	}
	if got, _ := c.Get("b", "same-key"); string(got) != "from-b" {
		t.Errorf("namespace b: got %q", got)
	}
}

func TestDisabledCacheIsNoOp(t *testing.T) {
	// A nil cache and an empty-dir cache both disable: Put does nothing, Get misses.
	for _, c := range []*Cache{nil, New(""), {}} {
		if c.Enabled() {
			t.Fatalf("%v should be disabled", c)
		}
		c.Put("cab", "k", []byte("x")) // must not panic
		if _, ok := c.Get("cab", "k"); ok {
			t.Errorf("disabled cache should always miss")
		}
	}
}

func TestOpenHonorsEnv(t *testing.T) {
	t.Setenv(envDisable, "1")
	if Open().Enabled() {
		t.Error("PEBUILD_NO_CACHE should disable the cache")
	}
	t.Setenv(envDisable, "")
	dir := t.TempDir()
	t.Setenv(envDir, dir)
	c := Open()
	if !c.Enabled() || c.Dir() != dir {
		t.Errorf("PEBUILD_CACHE_DIR not honored: enabled=%v dir=%q want %q", c.Enabled(), c.Dir(), dir)
	}
}

func TestPutIsAtomic(t *testing.T) {
	// After Put, only the final entry file exists — no leftover temp files that a
	// directory-scanning tool might mistake for entries.
	dir := t.TempDir()
	c := New(dir)
	c.Put("cab", "k", []byte("v"))
	entries, err := os.ReadDir(filepath.Join(dir, "cab"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one entry file, got %d", len(entries))
	}
}

func TestCorruptedEntryIsAMiss(t *testing.T) {
	// The filename is keyed by the source, not the content, so a damaged entry must
	// be rejected (miss → recompute) rather than served as a valid payload.
	dir := t.TempDir()
	c := New(dir)
	c.Put("cab", "k", []byte("the real member bytes"))
	entry := c.path("cab", "k")

	corruptions := map[string][]byte{
		"truncated below header": []byte("PBC"),
		"empty":                  {},
		"foreign file":           []byte("not a cache envelope at all"),
		"flipped payload byte":   flip(t, entry, entryHeader), // valid header, bad payload
		"flipped digest byte":    flip(t, entry, len(entryMagic)),
	}
	for name, bad := range corruptions {
		if err := os.WriteFile(entry, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := c.Get("cab", "k"); ok {
			t.Errorf("%s: Get reported a hit (%q); a corrupt entry must miss", name, got)
		}
	}

	// A re-Put after corruption restores a readable entry.
	c.Put("cab", "k", []byte("the real member bytes"))
	if got, ok := c.Get("cab", "k"); !ok || string(got) != "the real member bytes" {
		t.Fatalf("re-Put did not restore the entry: ok=%v got=%q", ok, got)
	}
}

// flip returns the entry file's bytes with the byte at off inverted, to simulate a
// single-bit/byte corruption of an otherwise well-formed envelope.
func flip(t *testing.T, path string, off int) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if off >= len(b) {
		t.Fatalf("offset %d past entry length %d", off, len(b))
	}
	out := append([]byte(nil), b...)
	out[off] ^= 0xff
	return out
}

func TestConcurrentPutGet(t *testing.T) {
	// The store must tolerate concurrent writers/readers (pewalk runs parallel
	// closure walks). Run with -race.
	c := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Put("cab", "shared", []byte("payload"))
			if got, ok := c.Get("cab", "shared"); ok && string(got) != "payload" {
				t.Errorf("concurrent read got %q", got)
			}
		}()
	}
	wg.Wait()
	if got, ok := c.Get("cab", "shared"); !ok || string(got) != "payload" {
		t.Fatalf("final read: ok=%v got=%q", ok, got)
	}
}
