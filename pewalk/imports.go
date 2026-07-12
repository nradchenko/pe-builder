// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"fmt"
	"io"
	"sort"

	pe "github.com/saferwall/pe"
	"github.com/saferwall/pe/log"
)

// silentLogger discards saferwall's internal diagnostics. Without an explicit
// logger, saferwall writes recovered-parse errors to os.Stdout — the same stream
// pewalk renders text/DOT/JSON to — which would corrupt machine-readable output.
// The parse outcome we care about is already surfaced through the returned error.
var silentLogger = log.NewStdLogger(io.Discard)

// importTables parses PE image bytes once and returns its static and delay-load
// import tables as two separate sorted, de-duplicated name sets. Names are
// canonicalized to their bare lowercased file name.
//
// The distinction matters to a caller computing a boot-necessary file set: a module's
// static imports are resolved by the loader when the module loads and must be present,
// whereas its delay imports are resolved lazily on first use and frequently never load
// in a minimal environment — so following them transitively over-includes. Keeping the
// two sets separate lets a caller cache this single parse and serve either walk mode
// from it via mergeImports — a module is PE-parsed at most once even across walks that
// follow different import sets.
//
// Parsing is limited to the two import directories: every other data directory is
// omitted, which keeps the parse fast and avoids the certificate and CLR paths
// that a stripped-down source module can trip over.
//
// saferwall recovers panics only within its data-directory parse; a malformed
// DOS/NT/section header can still panic. That panic is contained here so a single
// bad module degrades to a parse error rather than aborting the whole walk.
func importTables(data []byte) (static, delay []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			static, delay, err = nil, nil, fmt.Errorf("parse PE: panic: %v", r)
		}
	}()

	f, err := pe.NewBytes(data, &pe.Options{
		Logger:                     silentLogger,
		DisableCertValidation:      true,
		DisableSignatureValidation: true,
		OmitExportDirectory:        true,
		OmitExceptionDirectory:     true,
		OmitResourceDirectory:      true,
		OmitSecurityDirectory:      true,
		OmitRelocDirectory:         true,
		OmitDebugDirectory:         true,
		OmitArchitectureDirectory:  true,
		OmitGlobalPtrDirectory:     true,
		OmitTLSDirectory:           true,
		OmitLoadConfigDirectory:    true,
		OmitBoundImportDirectory:   true,
		OmitIATDirectory:           true,
		OmitCLRHeaderDirectory:     true,
		OmitCLRMetadata:            true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("parse PE: %w", err)
	}
	defer f.Close()
	if err := f.Parse(); err != nil {
		return nil, nil, fmt.Errorf("parse PE: %w", err)
	}

	// collect canonicalizes, de-duplicates, and sorts one import table's names.
	collect := func(emit func(add func(string))) []string {
		seen := map[string]struct{}{}
		var names []string
		emit(func(raw string) {
			name := moduleName(raw)
			if name == "" {
				return
			}
			if _, ok := seen[name]; ok {
				return
			}
			seen[name] = struct{}{}
			names = append(names, name)
		})
		sort.Strings(names)
		return names
	}
	static = collect(func(add func(string)) {
		for _, imp := range f.Imports {
			add(imp.Name)
		}
	})
	delay = collect(func(add func(string)) {
		for _, imp := range f.DelayImports {
			add(imp.Name)
		}
	})
	return static, delay, nil
}

// mergeImports combines a module's static and delay import sets for a walk mode and
// returns a freshly allocated slice the caller owns. With includeDelay false it
// returns a copy of the static set; otherwise the sorted union. It never returns a
// cached input by reference: a walk stores the result as a node's Imports, and the
// static/delay sets belong to a shared parse-cache entry, so handing back a reference
// would let a later in-place edit of one node's Imports corrupt the cache and every
// other node built from it.
func mergeImports(static, delay []string, includeDelay bool) []string {
	if !includeDelay || len(delay) == 0 {
		return append([]string(nil), static...)
	}
	seen := make(map[string]struct{}, len(static)+len(delay))
	out := make([]string, 0, len(static)+len(delay))
	for _, n := range static {
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, n := range delay {
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
