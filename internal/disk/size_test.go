// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package disk

import "testing"

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"512", 512},
		{"512b", 512},
		{"512B", 512},
		{"1k", 1024},
		{"1K", 1024},
		{"1kb", 1024},
		{"1KiB", 1024},
		{"256M", 256 << 20},
		{"256m", 256 << 20},
		{"256MB", 256 << 20},
		{"256MiB", 256 << 20},
		{"2G", 2 << 30},
		{"2GiB", 2 << 30},
		{"  256M  ", 256 << 20},
	}
	for _, tt := range tests {
		got, err := ParseSize(tt.in)
		if err != nil {
			t.Errorf("ParseSize(%q): unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestParseSizeBinary pins the decision that suffixes are binary. A decimal reading would
// pass every "is it a number" test while quietly producing a different disk.
func TestParseSizeBinary(t *testing.T) {
	got, err := ParseSize("256MB")
	if err != nil {
		t.Fatal(err)
	}
	if got != 268435456 {
		t.Errorf("ParseSize(\"256MB\") = %d, want 268435456 (binary); decimal would be 256000000", got)
	}
}

func TestParseSizeRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"0",
		"0M",
		"-1",
		"-1M",
		"abc",
		"M",
		"256T",
		"256 M",
		"1.5G",
		"0x100",
		"9223372036854775807G", // overflows
	} {
		if got, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", in, got)
		}
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{512, "512 bytes"},
		{1024, "1 KiB"},
		{256 << 20, "256 MiB"},
		{2 << 30, "2 GiB"},
		// A volume is its disk less the alignment gap, so a non-round number is the
		// ordinary case rather than the edge one.
		{256<<20 - alignmentGap, "255 MiB"},
		// An exact smaller unit is preferred over a rounded larger one. "3584 MiB" reads
		// less naturally than "3.5 GiB" but is exact, and this feeds a status line whose
		// whole job is to not overstate a size.
		{3<<30 + 512<<20, "3584 MiB"},
		// The case that decides the rule: a volume a megabyte under 2 GiB must not be
		// reported as "2.0 GiB".
		{2<<30 - alignmentGap, "2047 MiB"},
		// Only a size that divides no unit exactly falls back to a decimal.
		{1536*mib + 512, "1.5 GiB"},
	}
	for _, tt := range tests {
		if got := FormatSize(tt.in); got != tt.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
