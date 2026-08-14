// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	wim "github.com/nradchenko/go-wim"
)

// minimalSecurityDescriptor is a well-formed self-relative NT security descriptor with no owner,
// group, or ACLs — enough to satisfy go-wim's requirement that an image carry a non-empty
// security table. The content is irrelevant to reading the image back, which is all this test does.
func minimalSecurityDescriptor() []byte {
	sd := make([]byte, 20)                        // revision+sbz+control(4) + 4 zero offsets(16)
	sd[0] = 1                                     // revision
	binary.LittleEndian.PutUint16(sd[2:], 0x8000) // SE_SELF_RELATIVE, no owner/group/DACL/SACL
	return sd
}

// wimLayoutBacking must present a wim-layout image's PE.wim payload merged over its media root,
// so the inspection commands read through the WIM exactly as they read a classic image's tree.
// The WIM is a real one written by go-wim, so this exercises the actual reader, not a stand-in.
func TestWIMLayoutBackingReadsThroughPayload(t *testing.T) {
	// A tiny payload tree, captured into a WIM.
	tree := t.TempDir()
	writeTreeFile(t, tree, "i386/system32/ntoskrnl.exe", "KERNEL")
	writeTreeFile(t, tree, "i386/system32/config/software", "HIVE")
	wimPath := filepath.Join(t.TempDir(), wimImageFile)
	if err := wim.CaptureDir(context.Background(), tree, wimPath,
		wim.ImageInfo{Name: "XP", Boot: true},
		wim.Options{Security: wim.UniformSecurity(minimalSecurityDescriptor())}); err != nil {
		t.Fatalf("CaptureDir: %v", err)
	}
	wimBytes, err := os.ReadFile(wimPath)
	if err != nil {
		t.Fatal(err)
	}

	// A wim-layout media backing: the WIM plus the media-root files a real image carries.
	media := fstest.MapFS{
		wimImageFile:  {Data: wimBytes},
		"grldr":       {Data: []byte("loader")},
		"pebuild.txt": {Data: []byte("receipt")},
	}
	if !isWIMLayoutImage(media) {
		t.Fatal("media with PE.wim at root should be detected as wim-layout")
	}
	if !wimPayloadOpaque(media) {
		t.Fatal("a raw media backing is opaque until read through")
	}

	merged, err := wimLayoutBacking(media)
	if err != nil {
		t.Fatalf("wimLayoutBacking: %v", err)
	}
	if wimPayloadOpaque(merged) {
		t.Error("a merged backing is not opaque — the payload is readable, so the guardrail must stay quiet")
	}

	// The merged namespace lists both the payload tree and the media-root files.
	paths, err := walkImage(merged)
	if err != nil {
		t.Fatalf("walkImage: %v", err)
	}
	for _, want := range []string{"i386/system32/ntoskrnl.exe", "i386/system32/config/software", "grldr", "pebuild.txt", wimImageFile} {
		if !containsFold(paths, want) {
			t.Errorf("merged listing missing %q; got %v", want, paths)
		}
	}

	// A payload file resolves by bare name (case-insensitively) and reads its real bytes.
	from, err := resolveImageName(merged, paths, "ntoskrnl.exe")
	if err != nil {
		t.Fatalf("resolveImageName: %v", err)
	}
	if data, err := fs.ReadFile(merged, from); err != nil || string(data) != "KERNEL" {
		t.Fatalf("read payload %q: data=%q err=%v", from, data, err)
	}

	// A media-root file still reads too.
	if data, err := fs.ReadFile(merged, "grldr"); err != nil || string(data) != "loader" {
		t.Fatalf("read media grldr: data=%q err=%v", data, err)
	}
}

func writeTreeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsFold(paths []string, want string) bool {
	for _, p := range paths {
		if strings.EqualFold(p, want) {
			return true
		}
	}
	return false
}
