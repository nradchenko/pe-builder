// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

// A wim-layout image is recognised by PE.wim at the root (case-insensitive, since the ISO
// stage may uppercase it); a classic image, whose tree is on the filesystem directly, is not.
func TestIsWIMLayoutImage(t *testing.T) {
	wim := fstest.MapFS{
		"PE.wim":     {Data: []byte("MSWIM")},
		"coreimg.gz": {Data: []byte("gz")},
		"grldr":      {Data: []byte("loader")},
	}
	if !isWIMLayoutImage(wim) {
		t.Error("PE.wim at root: want wim-layout image")
	}
	upper := fstest.MapFS{"PE.WIM": {Data: []byte("MSWIM")}}
	if !isWIMLayoutImage(upper) {
		t.Error("PE.WIM (uppercased) at root: want wim-layout image")
	}
	classic := fstest.MapFS{
		"I386/SYSTEM32/NTOSKRNL.EXE": {Data: []byte("kernel")},
		"I386/NTDETECT.COM":          {Data: []byte("boot")},
	}
	if isWIMLayoutImage(classic) {
		t.Error("classic tree: want not a wim-layout image")
	}
}

// A payload file the caller names lives inside PE.wim, so it does not resolve — but on a
// wim-layout image the "not found" must carry the hint that explains where the payload is,
// rather than reading as a flat "that file is not here".
func TestResolveImageNamePayloadInWIMHint(t *testing.T) {
	wim := fstest.MapFS{"PE.wim": {Data: []byte("MSWIM")}}
	paths, err := walkFiles(wim)
	if err != nil {
		t.Fatalf("walkFiles: %v", err)
	}
	_, err = resolveImageName(wim, paths, "ntoskrnl.exe")
	if err == nil {
		t.Fatal("resolveImageName: want an error for a payload file sealed in the WIM")
	}
	if !strings.Contains(err.Error(), wimImageFile) {
		t.Errorf("error %q does not point at %s", err, wimImageFile)
	}

	// The media-root WIM itself still resolves — the guardrail must not block extracting it.
	from, err := resolveImageName(wim, paths, wimImageFile)
	if err != nil || from != wimImageFile {
		t.Fatalf("resolveImageName(%s): from=%q err=%v", wimImageFile, from, err)
	}
}
