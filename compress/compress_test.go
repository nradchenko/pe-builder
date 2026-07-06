// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package compress

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecompressRejectsNonCabinet(t *testing.T) {
	_, err := Decompress(bytes.NewReader([]byte("not a cabinet at all")))
	if err == nil {
		t.Fatal("expected an error for non-cabinet input")
	}
}

func TestDecompressRejectsMultiFileCabinet(t *testing.T) {
	// The single-shot Decompress helper only serves single-file cabinets; a
	// multi-file cabinet must direct the caller to ReadCabinet.
	_, err := Decompress(bytes.NewReader(readFixture(t, "lzx-multi.cab")))
	if err == nil || !strings.Contains(err.Error(), "ReadCabinet") {
		t.Fatalf("want a multi-file error mentioning ReadCabinet, got %v", err)
	}
}
