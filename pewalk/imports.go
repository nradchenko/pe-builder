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

// importsOf parses PE image bytes and returns the sorted, de-duplicated set of
// modules it imports. The static import table is always included; the delay-load
// import table is included only when includeDelay is set. Names are canonicalized to
// their bare lowercased file name.
//
// The distinction matters to a caller computing a boot-necessary file set: a module's
// static imports are resolved by the loader when the module loads and must be present,
// whereas its delay imports are resolved lazily on first use and frequently never load
// in a minimal environment — so following them transitively over-includes.
//
// Parsing is limited to the two import directories: every other data directory is
// omitted, which keeps the parse fast and avoids the certificate and CLR paths
// that a stripped-down source module can trip over.
//
// saferwall recovers panics only within its data-directory parse; a malformed
// DOS/NT/section header can still panic. That panic is contained here so a single
// bad module degrades to a parse error rather than aborting the whole walk.
func importsOf(data []byte, includeDelay bool) (names []string, err error) {
	defer func() {
		if r := recover(); r != nil {
			names, err = nil, fmt.Errorf("parse PE: panic: %v", r)
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
		return nil, fmt.Errorf("parse PE: %w", err)
	}
	defer f.Close()
	if err := f.Parse(); err != nil {
		return nil, fmt.Errorf("parse PE: %w", err)
	}

	seen := map[string]struct{}{}
	add := func(raw string) {
		name := moduleName(raw)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, imp := range f.Imports {
		add(imp.Name)
	}
	if includeDelay {
		for _, imp := range f.DelayImports {
			add(imp.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}
