// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nradchenko/pe-builder/internal/repo"
	"github.com/nradchenko/pe-builder/source"
)

// openISO opens the real source ISO as an io.ReaderAt for the boot-image parser.
// repo.SourceISO skips the test when the ISO is absent, so anything left for this
// function to report is a present-but-unreadable file, which is a failure.
func openISO(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(repo.SourceISO(t))
	if err != nil {
		t.Fatalf("open source ISO: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestExtractBootImage(t *testing.T) {
	img, err := ExtractBootImage(openISO(t))
	if err != nil {
		t.Fatal(err)
	}
	// XP install media carry a 2 KB no-emulation CDBOOT image (load-size 4).
	if len(img) != 2048 {
		t.Errorf("boot image is %d bytes, want 2048", len(img))
	}
	// It is Microsoft's CDBOOT sector that chains to \I386\SETUPLDR.BIN.
	for _, marker := range []string{"CDBOOT", "SETUPLDR.BIN"} {
		if !bytes.Contains(img, []byte(marker)) {
			t.Errorf("boot image does not contain %q", marker)
		}
	}
}

func TestWriteBootImage(t *testing.T) {
	out := t.TempDir()
	n, err := WriteBootImage(openISO(t), out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, BootSectName))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != n || n != 2048 {
		t.Errorf("wrote %d bytes, file has %d, want 2048", n, len(data))
	}
}

func TestExtractBootImageNonISO(t *testing.T) {
	// A file with no El Torito boot record must be rejected, not misread.
	r := bytes.NewReader(bytes.Repeat([]byte{0}, 40*1024))
	if _, err := ExtractBootImage(r); err == nil {
		t.Fatal("ExtractBootImage on a non-El-Torito reader returned no error")
	}
}

func TestWriteStartnet(t *testing.T) {
	root := t.TempDir()
	content := []byte("@echo off\r\necho hi>COM1\r\n")
	if err := WriteStartnet(root, source.Identity{Arch: "i386"}.Roots(), content); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "i386", "system32", "startnet.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("startnet.cmd = %q, want %q", got, content)
	}
}
