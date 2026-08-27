// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSniffFormat(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		// The real QCOW header begins with the magic and a version; only the magic is
		// read, so a header prefix is enough to pin the behaviour.
		{"qcow2", []byte{'Q', 'F', 'I', 0xfb, 0, 0, 0, 3}, FormatQCOW2},
		{"raw", make([]byte, 4096), FormatRaw},
		// Short files are raw rather than an error: it is how a small raw image answers.
		{"short", []byte{0, 1, 2}, FormatRaw},
		{"empty", nil, FormatRaw},
		// The magic must match in full — three of four bytes is not a qcow2.
		{"near-miss", []byte{'Q', 'F', 'I', 0xfc, 0, 0}, FormatRaw},
		// Containers this package cannot read still have to be *named*. Reporting them as
		// raw is what does the damage: a caller asserting format=raw to an emulator hands
		// the guest the container's header where its partition table should be.
		{"vmdk", []byte("KDMV\x01\x00\x00\x00"), "vmdk"},
		{"vdi", []byte("<<< Oracle VM VirtualBox Disk Image >>>\n"), "vdi"},
		{"vhdx", []byte("vhdxfile\x00\x00\x00\x00"), "vhdx"},
		// QEMU's driver name for VHD is "vpc", which is what the value is for.
		{"vhd", []byte("conectix\x00\x00\x00\x02"), "vpc"},
		{"qed", []byte("QED\x00\x00\x00\x00\x00"), "qed"},
		// A raw image whose first bytes merely start like one of them is still raw.
		{"not-quite-vmdk", []byte("KDM\x00padding"), FormatRaw},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".img")
			if err := os.WriteFile(path, tt.content, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := SniffFormat(path)
			if err != nil {
				t.Fatalf("SniffFormat: %v", err)
			}
			if got != tt.want {
				t.Errorf("SniffFormat(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestSniffFormatMissing(t *testing.T) {
	if _, err := SniffFormat(filepath.Join(t.TempDir(), "absent.img")); err == nil {
		t.Error("SniffFormat on a missing file: want an error")
	}
}

// TestOpenRefusesQCOW2 checks the refusal a caller actually sees, and that it points at
// the way out rather than only naming the problem.
func TestOpenRefusesQCOW2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(path, []byte{'Q', 'F', 'I', 0xfb, 0, 0, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("Open on a qcow2: want an error")
	}
	if !strings.Contains(err.Error(), "qemu-img convert") {
		t.Errorf("error %q does not say how to convert the image", err)
	}
}
