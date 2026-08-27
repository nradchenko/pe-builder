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

// newDisk makes a small formatted disk for a test to work against.
func newDisk(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scratch.img")
	if err := disk.Create(path, disk.Spec{Size: 16 << 20}); err != nil {
		t.Fatalf("create test disk: %v", err)
	}
	return path
}

// runDisk executes the disk command with the given arguments, returning what it wrote to
// stdout and stderr separately — which matters, because extracting to stdout has to stay
// pipeable and progress must not contaminate it.
func runDisk(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newDiskCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	// Cobra prints usage on error by default, which would drown the message under the
	// help text in every assertion below.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

func hostFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The round trip is the whole point of the command family: put a file where a guest will
// find it, and read back what a guest left.
func TestDiskAddListExtract(t *testing.T) {
	img := newDisk(t)
	src := hostFile(t, "hello.txt", "hello from the host")

	if _, _, err := runDisk(t, "add", img, src); err != nil {
		t.Fatalf("add: %v", err)
	}

	stdout, _, err := runDisk(t, "ls", img)
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(stdout, "hello.txt") {
		t.Errorf("ls does not show the added file:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 file(s) matched.") {
		t.Errorf("ls does not report one match:\n%s", stdout)
	}

	stdout, stderr, err := runDisk(t, "extract", img, "hello.txt", "-o", "-")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if stdout != "hello from the host" {
		t.Errorf("extract wrote %q to stdout", stdout)
	}
	// Progress belongs on stderr so that -o - stays a usable pipe.
	if stderr != "" {
		t.Errorf("extract to stdout also wrote to stderr: %q", stderr)
	}
}

// A disk holds what a guest wrote, so overwriting is opt-in rather than the default.
func TestDiskAddDoesNotOverwrite(t *testing.T) {
	img := newDisk(t)
	first := hostFile(t, "data.txt", "original")
	if _, _, err := runDisk(t, "add", img, first); err != nil {
		t.Fatal(err)
	}

	second := hostFile(t, "data.txt", "replacement")
	_, _, err := runDisk(t, "add", img, second)
	if err == nil {
		t.Fatal("adding over an existing file: want an error")
	}
	if !strings.Contains(err.Error(), "--replace") {
		t.Errorf("error %q does not name the flag that would allow it", err)
	}

	// The original must still be there — a refused add changes nothing.
	stdout, _, xerr := runDisk(t, "extract", img, "data.txt", "-o", "-")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if stdout != "original" {
		t.Errorf("the refused add changed the file to %q", stdout)
	}

	if _, _, err := runDisk(t, "add", img, second, "--replace"); err != nil {
		t.Fatalf("add --replace: %v", err)
	}
	stdout, _, xerr = runDisk(t, "extract", img, "data.txt", "-o", "-")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if stdout != "replacement" {
		t.Errorf("after --replace the file is %q", stdout)
	}
}

// A guest writes 8.3 short names, which are uppercase, so the file most likely to be on a
// disk is exactly the one a case-sensitive existence check misses. The write path folds
// case, so missing it does not mean adding a second file — it means silently replacing the
// guest's.
func TestDiskAddOverwriteGuardIsCaseInsensitive(t *testing.T) {
	img := newDisk(t)
	upper := hostFile(t, "HELLO.TXT", "written by the guest")
	if _, _, err := runDisk(t, "add", img, upper); err != nil {
		t.Fatal(err)
	}

	lower := hostFile(t, "hello.txt", "from the host")
	_, _, err := runDisk(t, "add", img, lower)
	if err == nil {
		t.Fatal("adding hello.txt over HELLO.TXT: want a refusal")
	}
	// The message should name the file as the disk spells it, so a case difference is
	// visible rather than looking like a refusal of the name that was typed.
	if !strings.Contains(err.Error(), "HELLO.TXT") {
		t.Errorf("error %q does not name the file as it is on the disk", err)
	}

	stdout, _, xerr := runDisk(t, "extract", img, "HELLO.TXT", "-o", "-")
	if xerr != nil {
		t.Fatal(xerr)
	}
	if stdout != "written by the guest" {
		t.Errorf("the guest's file was overwritten; it now reads %q", stdout)
	}
}

// Writing file bytes over a directory entry corrupts the volume — the entry keeps its
// subdirectory attribute, and a later walk recurses through the wreckage without end. It
// has to be refused whatever --replace says.
func TestDiskAddRefusesADirectory(t *testing.T) {
	img := newDisk(t)
	if _, _, err := runDisk(t, "add", img, hostFile(t, "inner.txt", "x"), "-d", "logs"); err != nil {
		t.Fatal(err)
	}

	// A host file named "logs" collides with the directory of that name on the disk.
	clash := hostFile(t, "logs", "not a directory")
	for _, args := range [][]string{
		{"add", img, clash},
		{"add", img, clash, "--replace"},
	} {
		_, _, err := runDisk(t, args...)
		if err == nil {
			t.Fatalf("%v: want a refusal, a directory must never be written over", args)
		}
		if !strings.Contains(err.Error(), "directory") {
			t.Errorf("error %q does not say the destination is a directory", err)
		}
	}

	// The volume must still be walkable — a corrupted directory entry shows up here as a
	// listing that never terminates.
	stdout, _, err := runDisk(t, "ls", img)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "logs/inner.txt") {
		t.Errorf("the directory did not survive intact:\n%s", stdout)
	}
}

// Reading must not require the ability to write: a disk on read-only media, or one
// copied out of a build, is still a disk worth inspecting.
func TestDiskReadOnlyFile(t *testing.T) {
	img := newDisk(t)
	if _, _, err := runDisk(t, "add", img, hostFile(t, "a.txt", "content")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(img, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(img, 0o644) })

	if _, _, err := runDisk(t, "ls", img); err != nil {
		t.Errorf("ls on a read-only disk: %v", err)
	}
	stdout, _, err := runDisk(t, "extract", img, "a.txt", "-o", "-")
	if err != nil {
		t.Errorf("extract from a read-only disk: %v", err)
	}
	if stdout != "content" {
		t.Errorf("extract read %q", stdout)
	}
}

// Everything that can fail is checked before anything is written, so a rejected batch
// leaves the disk exactly as it was rather than half filled.
func TestDiskAddIsAllOrNothing(t *testing.T) {
	img := newDisk(t)
	good := hostFile(t, "good.txt", "fine")
	missing := filepath.Join(t.TempDir(), "absent.txt")

	if _, _, err := runDisk(t, "add", img, good, missing); err == nil {
		t.Fatal("add with a missing host file: want an error")
	}

	stdout, _, err := runDisk(t, "ls", img)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "0 file(s) matched.") {
		t.Errorf("a refused batch left files on the disk:\n%s", stdout)
	}
}

// Two host files with one base name would leave only the last on the disk, which reads
// as a successful add of both.
func TestDiskAddRefusesCollidingNames(t *testing.T) {
	img := newDisk(t)
	a := hostFile(t, "same.txt", "a")
	b := hostFile(t, "same.txt", "b")

	_, _, err := runDisk(t, "add", img, a, b)
	if err == nil {
		t.Fatal("want an error for two files writing one name")
	}
	if !strings.Contains(err.Error(), "separately") {
		t.Errorf("error %q does not say what to do instead", err)
	}
}

func TestDiskAddDestDirectory(t *testing.T) {
	img := newDisk(t)
	src := hostFile(t, "boot.log", "line")

	if _, _, err := runDisk(t, "add", img, src, "-d", "logs/boot"); err != nil {
		t.Fatalf("add -d: %v", err)
	}
	stdout, _, err := runDisk(t, "ls", img)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "logs/boot/boot.log") {
		t.Errorf("the file did not land in the nested directory:\n%s", stdout)
	}
}

// A pattern names a base name or a whole path, exactly as it does for an image.
func TestDiskListPattern(t *testing.T) {
	img := newDisk(t)
	if _, _, err := runDisk(t, "add", img, hostFile(t, "a.txt", "a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDisk(t, "add", img, hostFile(t, "b.log", "b")); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runDisk(t, "ls", img, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "1 file(s) matched.") {
		t.Errorf("pattern *.txt did not match exactly one file:\n%s", stdout)
	}
	// A disk has no hint to add, so an empty result is the whole answer and must not
	// carry the note that belongs to a WIM-layout image.
	stdout, _, err = runDisk(t, "ls", img, "nosuch")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "Note:") {
		t.Errorf("a disk listing carried an image's hint:\n%s", stdout)
	}
}

func TestDiskExtractRefusesCollidingNames(t *testing.T) {
	img := newDisk(t)
	if _, _, err := runDisk(t, "add", img, hostFile(t, "same.txt", "a"), "-d", "one"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDisk(t, "add", img, hostFile(t, "same.txt", "b"), "-d", "two"); err != nil {
		t.Fatal(err)
	}

	_, _, err := runDisk(t, "extract", img, "one/same.txt", "two/same.txt", "-o", t.TempDir())
	if err == nil {
		t.Fatal("want an error for two files writing one output name")
	}
	if !strings.Contains(err.Error(), "separately") {
		t.Errorf("error %q does not say what to do instead", err)
	}
}

func TestDiskExtractStdoutRefusesSeveralNames(t *testing.T) {
	img := newDisk(t)
	if _, _, err := runDisk(t, "add", img, hostFile(t, "a.txt", "a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDisk(t, "add", img, hostFile(t, "b.txt", "b")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDisk(t, "extract", img, "a.txt", "b.txt", "-o", "-"); err == nil {
		t.Fatal("want an error for two names to stdout")
	}
}

func TestDiskCreate(t *testing.T) {
	dir := t.TempDir()

	t.Run("reports the volume, not the disk", func(t *testing.T) {
		p := filepath.Join(dir, "a.img")
		stdout, _, err := runDisk(t, "create", p, "--size", "256M")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		// 256 MiB asked for, 255 MiB usable: the alignment costs a megabyte and the
		// report must not round it back up.
		if !strings.Contains(stdout, "255 MiB") {
			t.Errorf("create does not report the usable volume size:\n%s", stdout)
		}
		if !strings.Contains(stdout, "FAT16") || !strings.Contains(stdout, disk.DefaultLabel) {
			t.Errorf("create does not report the filesystem and label:\n%s", stdout)
		}
	})

	t.Run("refuses an existing path", func(t *testing.T) {
		p := filepath.Join(dir, "b.img")
		if _, _, err := runDisk(t, "create", p, "--size", "16M"); err != nil {
			t.Fatal(err)
		}
		_, _, err := runDisk(t, "create", p, "--size", "16M")
		if err == nil {
			t.Fatal("want an error creating over an existing file")
		}
	})

	t.Run("rejects a bad size and a bad filesystem", func(t *testing.T) {
		if _, _, err := runDisk(t, "create", filepath.Join(dir, "c.img"), "--size", "banana"); err == nil {
			t.Error("want an error for an unparseable size")
		}
		if _, _, err := runDisk(t, "create", filepath.Join(dir, "d.img"), "--size", "16M", "--fs", "ntfs"); err == nil {
			t.Error("want an error for an unknown filesystem")
		}
		// Neither refusal should have left a file behind.
		for _, name := range []string{"c.img", "d.img"} {
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Errorf("a refused create left %s behind", name)
			}
		}
	})

	t.Run("takes a label", func(t *testing.T) {
		p := filepath.Join(dir, "e.img")
		stdout, _, err := runDisk(t, "create", p, "--size", "16M", "--label", "WORK")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "WORK") {
			t.Errorf("create does not report the label given:\n%s", stdout)
		}
	})
}

// The disk commands read any raw MBR+FAT image, so what they refuse should say why
// rather than failing obscurely.
func TestDiskOpenErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("not a disk", func(t *testing.T) {
		p := filepath.Join(dir, "text.img")
		if err := os.WriteFile(p, make([]byte, 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := runDisk(t, "ls", p)
		if err == nil || !strings.Contains(err.Error(), "partition") {
			t.Errorf("listing a non-disk: got %v, want an error about the partition table", err)
		}
	})

	t.Run("qcow2", func(t *testing.T) {
		p := filepath.Join(dir, "disk.qcow2")
		if err := os.WriteFile(p, []byte{'Q', 'F', 'I', 0xfb, 0, 0, 0, 3}, 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := runDisk(t, "ls", p)
		if err == nil || !strings.Contains(err.Error(), "qemu-img convert") {
			t.Errorf("listing a qcow2: got %v, want an error naming the conversion", err)
		}
	})
}

func TestNormalizeDiskDir(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"logs", "/logs"},
		{"/logs", "/logs"},
		{"logs/", "/logs"},
		{"logs/boot", "/logs/boot"},
		{`logs\boot`, "/logs/boot"},
		{`\logs\boot\`, "/logs/boot"},
	}
	for _, tt := range tests {
		got, err := normalizeDiskDir(tt.in)
		if err != nil {
			t.Errorf("normalizeDiskDir(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("normalizeDiskDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// A drive letter is the natural mistake once backslashes are accepted, and taken
	// literally it makes a directory named "D:" somewhere nobody asked for.
	for _, in := range []string{`D:\logs`, "D:/logs", "C:", `c:\`} {
		if got, err := normalizeDiskDir(in); err == nil {
			t.Errorf("normalizeDiskDir(%q) = %q, want an error naming the drive", in, got)
		}
	}
}
