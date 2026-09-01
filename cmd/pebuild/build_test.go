// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/build"
)

// A skipped file's line may claim only the check that failed — the stager found no copy
// to place. It must not assert the file is absent from the medium: a side-by-side
// assembly the layout never declares skips exactly like a file the medium lacks, and
// "not on this source" was believed for months about a file the media carry (gdiplus.dll).
func TestPrintReportSkipClaimsOnlyTheFailedCheck(t *testing.T) {
	var out strings.Builder
	rep := &build.Report{Written: 1, Skipped: []string{"gdiplus.dll"}}
	if err := printReport(&out, rep); err != nil {
		t.Fatalf("printReport: %v", err)
	}

	got := out.String()
	if want := "- gdiplus.dll (no stageable copy found)"; !strings.Contains(got, want) {
		t.Errorf("skip line: got report\n%s\nwant it to contain %q", got, want)
	}
	if stale := "not on this source"; strings.Contains(got, stale) {
		t.Errorf("skip line still claims absence from the medium: got report\n%s\nwant no %q", got, stale)
	}
}
