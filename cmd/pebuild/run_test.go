// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/source"
)

// TestMachineArgs covers the QEMU command line each combination of image and flags
// produces. The pairing that matters most is amd64 without KVM: the guest then gets
// QEMU's default 32-bit CPU model, and an x64 Windows will not boot on it.
//
// Each case pins the argv exactly rather than checking that the interesting tokens
// appear somewhere in it. A membership check passes on a flag paired with the wrong
// value — "-m 512 … 128" carries both "-m" and "128" — and on a flag emitted twice,
// which is most of what can actually go wrong when building a command line.
func TestMachineArgs(t *testing.T) {
	cases := []struct {
		name string
		m    machine
		want []string
	}{
		{
			name: "i386 with kvm",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, KVM: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-enable-kvm"},
		},
		{
			name: "i386 without kvm needs no cpu model",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso"},
		},
		{
			name: "amd64 without kvm advertises long mode",
			m:    machine{ISO: "pe.iso", Arch: source.ArchAMD64, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-cpu", "qemu64"},
		},
		{
			name: "amd64 with kvm takes the host cpu",
			m:    machine{ISO: "pe.iso", Arch: source.ArchAMD64, RAM: 512, KVM: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-enable-kvm"},
		},
		{
			name: "usb tablet",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, USBTablet: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-device", "usb-tablet"},
		},
		{
			name: "ram",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 128},
			want: []string{"-M", "pc", "-m", "128", "-boot", "d", "-cdrom", "pe.iso"},
		},
		{
			name: "serial attaches com1 for a serialdebug image",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, Serial: "unix:/tmp/kd.sock,server,nowait"},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso",
				"-serial", "unix:/tmp/kd.sock,server,nowait"},
		},
		{
			name: "no serial port unless asked for",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if args := tc.m.args(); !slices.Equal(args, tc.want) {
				t.Errorf("args =\n  %v\nwant\n  %v", args, tc.want)
			}
		})
	}
}

// TestQEMUBinariesFollowImageArch pins which emulators may boot which image. The
// binary follows the image, not the host: qemu-system-i386 presents a 32-bit CPU, so
// it must never be offered for an amd64 image, while a 32-bit image runs under either.
func TestQEMUBinariesFollowImageArch(t *testing.T) {
	amd64 := qemuBinaries[source.ArchAMD64]
	if slices.Contains(amd64, "qemu-system-i386") {
		t.Errorf("amd64 candidates = %v, must not include qemu-system-i386", amd64)
	}
	if len(amd64) == 0 || amd64[0] != "qemu-system-x86_64" {
		t.Errorf("amd64 candidates = %v, want qemu-system-x86_64 first", amd64)
	}

	i386 := qemuBinaries[source.ArchI386]
	want := []string{"qemu-system-i386", "qemu-system-x86_64"}
	if !slices.Equal(i386, want) {
		t.Errorf("i386 candidates = %v, want %v", i386, want)
	}
}

// TestFindQEMUUnknownArch covers an architecture no candidate list carries: it must
// report that rather than falling through to an emulator that cannot boot the image.
func TestFindQEMUUnknownArch(t *testing.T) {
	if got, err := findQEMU("ia64"); err == nil {
		t.Errorf("findQEMU(ia64) = %q, want an error", got)
	}
}

// TestIsImage covers telling an image from a manifest. The two are told apart by
// content, so the cases that matter are the ones where the name disagrees with it:
// a manifest saved as .iso must not be booted, and an image is an image whatever it
// is called.
func TestIsImage(t *testing.T) {
	dir := t.TempDir()

	image := filepath.Join(dir, "pe.bin")
	writeFakeISO(t, image)

	imageNamedHCL := filepath.Join(dir, "pe.hcl")
	writeFakeISO(t, imageNamedHCL)

	manifestNamedISO := filepath.Join(dir, "manifest.iso")
	if err := os.WriteFile(manifestNamedISO, []byte("output {\n  target = \"iso\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Shorter than the offset the descriptor is read from: not an image, and the
	// short read must not be mistaken for one.
	short := filepath.Join(dir, "short.iso")
	if err := os.WriteFile(short, []byte("CD001"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want bool
	}{
		{image, true},
		{imageNamedHCL, true},
		{manifestNamedISO, false},
		{short, false},
	}
	for _, tc := range cases {
		got, err := isImage(tc.path)
		if err != nil {
			t.Errorf("isImage(%s): %v", filepath.Base(tc.path), err)
			continue
		}
		if got != tc.want {
			t.Errorf("isImage(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}
}

// TestIsImageRejectsUnreadable covers the arguments that are neither an image nor a
// manifest. Each has to be reported as itself: falling through to the manifest reader
// would blame a mistyped image path on manifest syntax, which is what a user who
// misspells an .iso would otherwise be told.
func TestIsImageRejectsUnreadable(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		path string
	}{
		{"absent", filepath.Join(dir, "absent.iso")},
		{"directory", dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isImage(tc.path)
			if err == nil {
				t.Fatalf("isImage(%s) = %v, want an error", tc.name, got)
			}
			// Naming the manifest as the other thing an argument may be is fine;
			// what must not happen is the argument reaching the manifest reader
			// and coming back as a parse failure.
			if strings.Contains(err.Error(), "read manifest") {
				t.Errorf("error = %v, must not report a manifest read failure", err)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("error = %v, want it to name %s", err, tc.path)
			}
		})
	}
}

// TestKVMStatus covers the three answers the acceleration check gives, and which of
// them the user hears about. An absent device is normal — a container, a CI runner —
// and must stay silent; a device that exists and will not open is a fixable
// permission problem, and silence there costs an order of magnitude in speed while
// looking like the tool is simply slow.
func TestKVMStatus(t *testing.T) {
	dir := t.TempDir()

	t.Run("absent is silent", func(t *testing.T) {
		kvm, notice := kvmStatusAt(filepath.Join(dir, "no-such-device"))
		if kvm || notice != "" {
			t.Errorf("kvmStatusAt(absent) = (%v, %q), want (false, \"\")", kvm, notice)
		}
	})

	t.Run("openable", func(t *testing.T) {
		p := filepath.Join(dir, "openable")
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		kvm, notice := kvmStatusAt(p)
		if !kvm || notice != "" {
			t.Errorf("kvmStatusAt(openable) = (%v, %q), want (true, \"\")", kvm, notice)
		}
	})

	t.Run("present but not permitted warns", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root opens a 0000 file, so the permission case cannot be staged")
		}
		p := filepath.Join(dir, "forbidden")
		if err := os.WriteFile(p, nil, 0o000); err != nil {
			t.Fatal(err)
		}
		kvm, notice := kvmStatusAt(p)
		if kvm {
			t.Error("kvmStatusAt(unopenable) reported acceleration")
		}
		if notice == "" {
			t.Fatal("kvmStatusAt(unopenable) gave no notice; a fixable permission problem must be visible")
		}
		if !strings.Contains(notice, "kvm group") {
			t.Errorf("notice = %q, want it to name the fix", notice)
		}
	})
}

// TestResolveImageManifestErrors covers the two manifests that name no image to boot.
// Both have to say so in terms of the build that would produce one, since run never
// builds: a bare "no such file" would read as a broken manifest.
func TestResolveImageManifestErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	const src = "source {\n  path = \"/nonexistent\"\n}\n"

	unbuilt := write("unbuilt.hcl", src+
		"output {\n  target = \"iso\"\n  path = \""+filepath.Join(dir, "never-built.iso")+"\"\n}\n")
	if _, err := resolveImage(unbuilt); err == nil {
		t.Error("resolveImage on a manifest whose image was never built returned no error")
	} else if !strings.Contains(err.Error(), "build it first") {
		t.Errorf("error = %v, want it to point at building the image", err)
	}

	none := write("none.hcl", src+"output {\n  target = \"none\"\n  path = \"\"\n}\n")
	if _, err := resolveImage(none); err == nil {
		t.Error("resolveImage on a target that builds no media returned no error")
	} else if !strings.Contains(err.Error(), "nothing to boot") {
		t.Errorf("error = %v, want it to say the target builds no media", err)
	}
}

// TestResolveImageFromManifest covers the path that resolves: a manifest whose image
// exists returns that image, not the manifest.
func TestResolveImageFromManifest(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "pe.iso")
	writeFakeISO(t, iso)

	m := filepath.Join(dir, "pebuild.hcl")
	body := "source {\n  path = \"/nonexistent\"\n}\n" +
		"output {\n  target = \"iso\"\n  path = \"" + iso + "\"\n}\n"
	if err := os.WriteFile(m, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveImage(m)
	if err != nil {
		t.Fatal(err)
	}
	if got != iso {
		t.Errorf("resolveImage = %q, want the manifest's output.path %q", got, iso)
	}
}

// writeFakeISO writes a file carrying an ISO9660 volume descriptor where one belongs.
// It is enough for the content check that tells an image from a manifest; nothing
// here reads an image's filesystem.
func writeFakeISO(t *testing.T, path string) {
	t.Helper()
	buf := make([]byte, isoDescriptorOffset+len(isoDescriptorID))
	copy(buf[isoDescriptorOffset:], isoDescriptorID)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}
