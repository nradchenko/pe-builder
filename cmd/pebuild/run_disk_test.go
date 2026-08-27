// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/disk"
)

// TestScratchPath pins where a scratch disk lands: beside the image it belongs to and
// named after it, so it is as findable as the image is. A second one on the same boot
// gets its own name rather than colliding with the first.
func TestScratchPath(t *testing.T) {
	tests := []struct {
		iso  string
		n    int
		want string
	}{
		{"out/pe.iso", 0, "out/pe-scratch.img"},
		{"out/pe.iso", 1, "out/pe-scratch2.img"},
		{"out/pe.iso", 2, "out/pe-scratch3.img"},
		{"/tmp/x/image.iso", 0, "/tmp/x/image-scratch.img"},
		// An image with no extension, and one with several dots, both have to produce
		// something sane rather than eating part of the name.
		{"out/pe", 0, "out/pe-scratch.img"},
		{"out/pe.v2.iso", 0, "out/pe.v2-scratch.img"},
	}
	for _, tt := range tests {
		if got := scratchPath(tt.iso, tt.n); got != tt.want {
			t.Errorf("scratchPath(%q, %d) = %q, want %q", tt.iso, tt.n, got, tt.want)
		}
	}
}

// TestDriveIndexStepsOverTheCD pins the slot mapping against what QEMU actually accepts.
//
// Measured on qemu-system-i386 with -M pc: the IDE controller carries indices 0 to 3 and
// nothing more, and `-cdrom` is itself a drive at index 2 on that same interface. So the
// three usable disk slots are 0, 1 and 3, and a disk placed at 2 stops the machine
// starting rather than being quietly moved.
func TestDriveIndexStepsOverTheCD(t *testing.T) {
	tests := []struct{ n, want int }{
		{0, 0},
		{1, 1},
		{2, 3},
	}
	for _, tt := range tests {
		if got := driveIndex(tt.n); got != tt.want {
			t.Errorf("driveIndex(%d) = %d, want %d", tt.n, got, tt.want)
		}
		if driveIndex(tt.n) == cdromIndex {
			t.Errorf("driveIndex(%d) collides with the CD's slot", tt.n)
		}
	}
}

// More disks than the controller has slots is refused here, where the message can name
// the limit, rather than by QEMU naming an IDE bus and unit the caller never mentioned.
func TestResolveDrivesRefusesTooManyDisks(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, n := range []string{"a", "b", "c", "d"} {
		p := filepath.Join(dir, n+".img")
		if err := os.WriteFile(p, make([]byte, 512), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	var out, errOut strings.Builder
	_, err := resolveDrives(&out, &errOut, filepath.Join(dir, "pe.iso"), paths, nil)
	if err == nil {
		t.Fatal("want an error for more disks than the machine can carry")
	}
	if !strings.Contains(err.Error(), "at most") {
		t.Errorf("error %q does not name the limit", err)
	}

	// The limit counts both flags together, since they share the one controller.
	out.Reset()
	errOut.Reset()
	if _, err := resolveDrives(&out, &errOut, filepath.Join(dir, "pe.iso"),
		paths[:2], []int64{16 << 20, 16 << 20}); err == nil {
		t.Error("want an error when the two flags together exceed the limit")
	}
}

// Attaching one host file twice gives the guest two independent writable views of it.
// The natural way to do it by accident is to name the scratch disk with --disk as well,
// where the two paths differ by spelling rather than by file.
func TestResolveDrivesRefusesTheSameFileTwice(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "pe.iso")
	a := filepath.Join(dir, "a.img")
	if err := os.WriteFile(a, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	if _, err := resolveDrives(&out, &errOut, iso, []string{a, a}, nil); err == nil {
		t.Error("want an error for the same --disk twice")
	}

	// The same file reached by two spellings is still one file.
	out.Reset()
	errOut.Reset()
	viaDot := filepath.Join(dir, ".", "a.img")
	if _, err := resolveDrives(&out, &errOut, iso, []string{a, viaDot}, nil); err == nil {
		t.Error("want an error for one file named two ways")
	}

	// And the scratch disk named explicitly alongside --scratch-disk.
	out.Reset()
	errOut.Reset()
	scratch := filepath.Join(dir, "pe-scratch.img")
	if err := disk.Create(scratch, disk.Spec{Size: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDrives(&out, &errOut, iso, []string{scratch}, []int64{16 << 20}); err == nil {
		t.Error("want an error when --disk names the file --scratch-disk derives")
	}
}

// TestResolveDrivesOrder pins the stated rule: attached disks first, then scratch disks,
// each in the order given. The command line's own interleaving is not recoverable — the
// two flags parse into separate slices — so the rule is what the docs assert, and this is
// what holds them to it.
func TestResolveDrivesOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.img")
	b := filepath.Join(dir, "b.img")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, make([]byte, 512), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	iso := filepath.Join(dir, "pe.iso")

	var out, errOut strings.Builder
	drives, err := resolveDrives(&out, &errOut, iso, []string{a, b}, []int64{16 << 20})
	if err != nil {
		t.Fatalf("resolveDrives: %v", err)
	}
	want := []string{a, b, filepath.Join(dir, "pe-scratch.img")}
	if len(drives) != len(want) {
		t.Fatalf("got %d drives, want %d", len(drives), len(want))
	}
	for i, w := range want {
		if drives[i].Path != w {
			t.Errorf("drive %d is %s, want %s", i, drives[i].Path, w)
		}
	}
}

// A qcow2 must reach the emitted argument as qcow2. The format is decided from the file's
// own bytes, so this also covers that the sniff is wired in at all.
func TestResolveDrivesSniffsFormat(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.img")
	qcow := filepath.Join(dir, "disk.qcow2")
	if err := os.WriteFile(raw, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qcow, []byte{'Q', 'F', 'I', 0xfb, 0, 0, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	drives, err := resolveDrives(&out, &errOut, filepath.Join(dir, "pe.iso"),
		[]string{raw, qcow}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if drives[0].Format != disk.FormatRaw {
		t.Errorf("raw image reported as %q", drives[0].Format)
	}
	if drives[1].Format != disk.FormatQCOW2 {
		t.Errorf("qcow2 image reported as %q", drives[1].Format)
	}
}

func TestResolveDrivesMissingDisk(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.img")
	var out, errOut strings.Builder
	_, err := resolveDrives(&out, &errOut, filepath.Join(dir, "pe.iso"), []string{missing}, nil)
	if err == nil {
		t.Fatal("want an error for a --disk that does not exist")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the missing path", err)
	}
}

// The persistence rule, which is the one with data behind it: an existing scratch disk is
// reused and never rewritten, whatever size is asked for. The assertion is on the file's
// bytes, because that is what "never rewritten" actually means.
func TestResolveDrivesReusesAndNeverRewrites(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "pe.iso")
	scratch := filepath.Join(dir, "pe-scratch.img")

	var out, errOut strings.Builder
	if _, err := resolveDrives(&out, &errOut, iso, nil, []int64{16 << 20}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !strings.Contains(out.String(), "created") {
		t.Errorf("the first run does not report creating the disk:\n%s", out.String())
	}

	// Put something on it, as a guest would.
	img, err := disk.Open(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if err := img.WriteFile("guest.txt", []byte("written by the guest")); err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(scratch)
	if err != nil {
		t.Fatal(err)
	}

	// Ask for a different size. The disk must be reused exactly as it stands.
	out.Reset()
	errOut.Reset()
	if _, err := resolveDrives(&out, &errOut, iso, nil, []int64{64 << 20}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out.String(), "reusing") {
		t.Errorf("the second run does not report reusing the disk:\n%s", out.String())
	}
	// The mismatch must be visible rather than silently swallowed.
	if !strings.Contains(errOut.String(), "ignored") {
		t.Errorf("the ignored size was not reported:\n%s", errOut.String())
	}

	after, err := os.ReadFile(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the existing scratch disk was rewritten")
	}
}

// A path taken by something else is an error, not a silent replacement: the file may be
// anything, and destroying it to make room for a scratch disk is never what was asked.
func TestResolveDrivesRefusesANonDiskAtTheDerivedPath(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "pe.iso")
	other := filepath.Join(dir, "pe-scratch.img")
	if err := os.WriteFile(other, []byte("something else entirely"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	_, err := resolveDrives(&out, &errOut, iso, nil, []int64{16 << 20})
	if err == nil {
		t.Fatal("want an error when the derived path holds something that is not a disk")
	}
	if !strings.Contains(err.Error(), "not a disk") {
		t.Errorf("error %q does not say what is wrong", err)
	}
	// And it must still be there afterwards.
	got, rerr := os.ReadFile(other)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "something else entirely" {
		t.Errorf("the file at the derived path was modified: %q", got)
	}
}

// The scratch disk a run creates has to be one the disk commands can read: they are the
// only way to see what the guest wrote, so a disk `run` makes but `disk ls` cannot open
// would make the whole feature unusable in the direction that matters.
func TestScratchDiskIsReadableByTheDiskCommands(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "pe.iso")

	var out, errOut strings.Builder
	drives, err := resolveDrives(&out, &errOut, iso, nil, []int64{16 << 20})
	if err != nil {
		t.Fatal(err)
	}

	img, err := disk.OpenRead(drives[0].Path)
	if err != nil {
		t.Fatalf("a disk run created cannot be read back: %v", err)
	}
	defer img.Close()
	if info := img.Info(); info.Label != disk.DefaultLabel {
		t.Errorf("label = %q, want %q", info.Label, disk.DefaultLabel)
	}
}
