// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/repo"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// TestProductLine covers what `source probe` says about the product type. The verdict
// alone is not the output — which of the media's two answers produced it is, because a
// SKU nobody has measured settles on the workstation answer and must not read as one
// the media stated.
func TestProductLine(t *testing.T) {
	tests := []struct {
		name string
		lay  *layout.Layout
		want string
	}{{
		name: "declared workstation",
		lay: &layout.Layout{ProductEvidence: layout.ProductEvidence{
			Answer: layout.ProductAnswerDeclared, Value: "WinNt", Recognized: true,
		}},
		want: "workstation   (hivesys.inf declares WinNt)",
	}, {
		name: "declared server",
		lay: &layout.Layout{ServerProduct: true, ProductEvidence: layout.ProductEvidence{
			Answer: layout.ProductAnswerDeclared, Value: "ServerNT", Recognized: true,
		}},
		want: "server        (hivesys.inf declares ServerNT)",
	}, {
		name: "measured SKU",
		lay: &layout.Layout{ServerProduct: true, ProductEvidence: layout.ProductEvidence{
			Answer: layout.ProductAnswerSetupData, Value: "1", Recognized: true,
		}},
		want: "server        ([SetupData] ProductType = 1)",
	}, {
		// The case the evidence exists for: the verdict is a default, not a reading.
		name: "unmeasured SKU is named as a fallback",
		lay: &layout.Layout{ProductEvidence: layout.ProductEvidence{
			Answer: layout.ProductAnswerSetupData, Value: "9", Recognized: false,
		}},
		want: "workstation   ([SetupData] ProductType = 9, an unrecognized SKU; defaulted)",
	}, {
		// A declaration this build cannot read, with no SKU behind it. The media did
		// answer; reporting silence here would credit them with saying nothing.
		name: "unreadable declaration is not silence",
		lay: &layout.Layout{ProductEvidence: layout.ProductEvidence{
			Answer: layout.ProductAnswerDeclared, Value: "EmbeddedNT", Recognized: false,
		}},
		want: "workstation   (hivesys.inf declares EmbeddedNT, which is not a product type this build knows; defaulted)",
	}, {
		name: "media state neither answer",
		lay:  &layout.Layout{},
		want: "workstation   (the media state neither answer; defaulted)",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := productLine(tt.lay); got != tt.want {
				t.Errorf("productLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProbeUnreadableLayoutStillIdentifies covers a source that identifies but whose
// setup metadata will not parse. Build and architecture come from the source folder and
// ntdll, so they are still true; failing the whole command over the third answer would
// withhold two answers that were obtained. The missing one is reported as unknown.
func TestProbeUnreadableLayoutStillIdentifies(t *testing.T) {
	// A directory source carrying just enough for Probe: an i386 folder and the ntdll it
	// reads a version from, taken from real media at run time rather than committed. No
	// txtsetup.sif, so layout.Parse cannot succeed.
	media, err := source.Open(repo.SourceISO(t))
	if err != nil {
		t.Fatal(err)
	}
	id, err := media.Probe()
	if err != nil {
		t.Fatal(err)
	}
	data, packed, found := layout.ReadSourceFile(media.Backing(), id.Roots(), source.AreaSystem, "ntdll.dll")
	if !found {
		t.Fatal("no ntdll on the test source")
	}

	dir := t.TempDir()
	i386 := filepath.Join(dir, "i386")
	if err := os.MkdirAll(i386, 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep the form the media stores it in: Probe reads either, and decompressing here
	// would test this test's decompression rather than the command's.
	name := "ntdll.dll"
	if packed {
		name = layout.CompressedName(name)
	}
	if err := os.WriteFile(filepath.Join(i386, name), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSourceProbeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("probe failed on a source whose layout cannot be parsed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Build:   2600", "Arch:    i386", "Product: unknown"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
