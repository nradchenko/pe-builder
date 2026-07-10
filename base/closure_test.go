// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/source"
)

// realSource opens the Windows source used for the closure regression. It is a real
// installation medium, not a fixture, because the closure's regression target — the
// known-good boot file set — is a property of a real source. The path comes from
// PEBUILD_TEST_SOURCE, falling back to the developer cache; the test skips when
// neither is present, so `go test ./...` stays green without the (git-ignored) media.
func realSource(t *testing.T) (source.Backing, source.Identity, *layout.Layout, *pewalk.Source) {
	t.Helper()
	path := os.Getenv("PEBUILD_TEST_SOURCE")
	if path == "" {
		cache := filepath.Join("..", ".cache", "ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso")
		if _, err := os.Stat(cache); err == nil {
			path = cache
		}
	}
	if path == "" {
		t.Skip("no Windows source: set PEBUILD_TEST_SOURCE to an XP source (dir or iso)")
	}

	src, err := source.Open(path)
	if err != nil {
		t.Fatalf("open source %s: %v", path, err)
	}
	id, err := src.Probe()
	if err != nil {
		t.Fatalf("probe source: %v", err)
	}
	l, err := layout.Parse(src.Backing(), id)
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	pw, err := pewalk.Open(src.Backing())
	if err != nil {
		t.Fatalf("index source: %v", err)
	}
	return src.Backing(), id, l, pw
}

// copyNames returns the lowercased source names copied by a set of actions.
func copyNames(acts []action.Action) map[string]bool {
	out := make(map[string]bool, len(acts))
	for _, a := range acts {
		if a.Kind == action.CopyFile {
			out[strings.ToLower(a.Src)] = true
		}
	}
	return out
}

// TestClosureResolvesCoreRuntime proves the import scanner works against a real
// source: the core Win32 runtime DLLs are reached from the load roots and every one
// resolves in the source (0 missing). These are the load-bearing dependencies the
// hand-list no longer needs to name.
func TestClosureResolvesCoreRuntime(t *testing.T) {
	_, _, l, pw := realSource(t)

	acts, _, err := ClosureActions(pw, l)
	if err != nil {
		t.Fatal(err)
	}
	covered := copyNames(acts)

	// A reviewed core: subsystem DLLs and the LSA/RPC chain that are statically imported
	// (transitively) from the load roots — so the static closure must cover every one.
	// Delay-only dependencies (e.g. oleaut32) are deliberately excluded.
	core := []string{
		"kernel32.dll", "advapi32.dll", "user32.dll", "gdi32.dll", "rpcrt4.dll",
		"shell32.dll", "shlwapi.dll", "ole32.dll", "secur32.dll", "comctl32.dll",
		"lsasrv.dll", "samlib.dll", "netapi32.dll", "userenv.dll", "crypt32.dll",
	}
	for _, name := range core {
		if !covered[name] {
			t.Errorf("closure does not cover core runtime DLL %q", name)
		}
	}
}

// TestBootFilesetNoNetMissing is the durable 0-net-missing guard: every file in the
// frozen known-good boot set must still be produced by the base layer — whether by
// the hand-curated seed (base.inf), the source-derived drivers/locale, or the
// computed import closure. As entries move out of base.inf into the closure, this
// proves nothing needed is dropped. A source-absent name is a self-sufficiency red
// herring, allow-listed here rather than treated as a regression.
func TestBootFilesetNoNetMissing(t *testing.T) {
	_, id, l, pw := realSource(t)

	// Exercise the real production path: the full base action set the build assembles
	// (closure ∪ base.inf seed ∪ boot drivers ∪ locale). Deriving the produced set from
	// the actual copy actions — rather than adding the seeds and drivers by name — keeps
	// the guard honest: a load root dropped from base.inf (the closure only places the
	// roots' *dependencies*, not the roots) shows up here as net-missing.
	allActs, err := Actions(pw, l, id)
	if err != nil {
		t.Fatalf("base actions: %v", err)
	}
	produced := copyNames(allActs)

	want := readFileset(t, filepath.Join("testdata", "boot-fileset.golden"))
	var missing []string
	for _, name := range want {
		if !produced[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d known-good boot file(s) net-missing from the computed set: %s",
			len(missing), strings.Join(missing, " "))
	}

	// Report the reduction headroom: how much of the known-good set the closure alone
	// covers, i.e. how far base.inf could shrink.
	closureActs, _, err := ClosureActions(pw, l)
	if err != nil {
		t.Fatalf("closure actions: %v", err)
	}
	closureCovered := copyNames(closureActs)
	var byClosure int
	for _, name := range want {
		if closureCovered[name] {
			byClosure++
		}
	}
	t.Logf("known-good set: %d files; covered by import closure alone: %d", len(want), byClosure)
}

// readFileset reads a newline-separated, lowercased file list, ignoring blanks and
// comments.
func readFileset(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("read fileset: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
