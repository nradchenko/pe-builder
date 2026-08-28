// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wim "github.com/nradchenko/go-wim"
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
//
// Most cases carry no adapter and so end in "-nic none", which is a positive
// instruction to QEMU rather than an absence: left to itself it gives the machine an
// e1000 no supported medium has a driver for.
func TestMachineArgs(t *testing.T) {
	cases := []struct {
		name string
		m    machine
		want []string
	}{
		{
			name: "i386 with kvm",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, KVM: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-enable-kvm",
				"-nic", "none"},
		},
		{
			name: "i386 without kvm needs no cpu model",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-nic", "none"},
		},
		{
			name: "amd64 without kvm advertises long mode",
			m:    machine{ISO: "pe.iso", Arch: source.ArchAMD64, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-cpu", "qemu64",
				"-nic", "none"},
		},
		{
			name: "amd64 with kvm takes the host cpu",
			m:    machine{ISO: "pe.iso", Arch: source.ArchAMD64, RAM: 512, KVM: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-enable-kvm",
				"-nic", "none"},
		},
		{
			// The tablet brings its own controller: the "pc" machine has no USB bus,
			// so a lone -device usb-tablet is refused before the guest ever starts.
			name: "usb tablet",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, USBTablet: true},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso",
				"-device", "usb-ehci,id=ehci", "-device", "usb-tablet,bus=ehci.0", "-nic", "none"},
		},
		{
			// Declining the tablet adds no pointer device at all — the PS/2 mouse it
			// falls back to is part of the machine and needs no argument.
			name: "ps2 mouse adds nothing",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, USBTablet: false},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-nic", "none"},
		},
		{
			name: "ram",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 128},
			want: []string{"-M", "pc", "-m", "128", "-boot", "d", "-cdrom", "pe.iso", "-nic", "none"},
		},
		{
			// The adapter and its backend are one device, not a model dropped onto
			// whatever QEMU would have connected: the -device names the netdev by id.
			name: "adapter on user-mode networking",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, NIC: "rtl8139"},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso",
				"-device", "rtl8139,netdev=net0", "-netdev", "user,id=net0"},
		},
		{
			name: "another adapter model",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, NIC: "pcnet"},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso",
				"-device", "pcnet,netdev=net0", "-netdev", "user,id=net0"},
		},
		{
			// The capture taps the backend, so it must name the same netdev id the
			// adapter was attached to — a filter on any other id records nothing.
			name: "capture taps the adapter's backend",
			m: machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, NIC: "rtl8139",
				Pcap: "/tmp/dhcp.pcap"},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso",
				"-device", "rtl8139,netdev=net0", "-netdev", "user,id=net0",
				"-object", "filter-dump,id=pcap0,netdev=net0,file=/tmp/dhcp.pcap"},
		},
		{
			name: "serial attaches com1 for a serialdebug image",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512, Serial: "unix:/tmp/kd.sock,server,nowait"},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-nic", "none",
				"-serial", "unix:/tmp/kd.sock,server,nowait"},
		},
		{
			name: "no serial port unless asked for",
			m:    machine{ISO: "pe.iso", Arch: source.ArchI386, RAM: 512},
			want: []string{"-M", "pc", "-m", "512", "-boot", "d", "-cdrom", "pe.iso", "-nic", "none"},
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

// TestPointerFlagDefault pins the shape of the pointer flag: an opt-out named
// --ps2-mouse that is off by default, so a plain run gets the tablet. The argv tests
// above set machine.USBTablet directly and so say nothing about which way the flag
// points; this is the half of the default that lives in the command's surface.
//
// It does not reach the RunE inversion that turns the flag into a machine, which
// needs a real image and a QEMU on PATH to exercise.
func TestPointerFlagDefault(t *testing.T) {
	f := newRunCmd().Flags().Lookup("ps2-mouse")
	if f == nil {
		t.Fatal("no --ps2-mouse flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--ps2-mouse defaults to %q, want %q (the USB tablet is the default pointer)",
			f.DefValue, "false")
	}
	if got := newRunCmd().Flags().Lookup("usb-tablet"); got != nil {
		t.Error("--usb-tablet still registered; the tablet is the default and --ps2-mouse opts out")
	}
}

// TestNICFlagDefault pins the adapter the command offers when asked for nothing in
// particular. The default is the point of the flag rather than a convenience: QEMU's
// own default for this machine is an e1000, which no supported medium carries a driver
// for, so an image that configures networking at boot finds no adapter it can bind and
// the feature looks broken. RTL8139 is the model every supported source drives.
func TestNICFlagDefault(t *testing.T) {
	f := newRunCmd().Flags().Lookup("nic")
	if f == nil {
		t.Fatal("no --nic flag")
	}
	if f.DefValue != "rtl8139" {
		t.Errorf("--nic defaults to %q, want %q (the one model every supported source drives)",
			f.DefValue, "rtl8139")
	}
}

// TestNICModel covers the one value --nic reads rather than passes through: "none"
// selects the machine's empty model, which is what emits -nic none. Spelling the
// opt-out as a value keeps it out of a negated flag name.
func TestNICModel(t *testing.T) {
	if got := nicModel("none"); got != "" {
		t.Errorf("nicModel(none) = %q, want the empty model", got)
	}
	for _, model := range []string{"rtl8139", "pcnet", "ne2k_pci", "e1000"} {
		if got := nicModel(model); got != model {
			t.Errorf("nicModel(%q) = %q, want it passed through", model, got)
		}
	}
}

// runFlagError runs the command with the given flags against an image path that does
// not exist, and returns whatever it refused with. Every flag check runs before the
// image is resolved, so a check that fires is the error and one that does not lets
// the missing image be the error — which is what keeps these tests from passing on
// the wrong refusal.
func runFlagError(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newRunCmd()
	cmd.SetArgs(append(args, "pe.iso"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("run %v was accepted", args)
	}
	return err
}

// TestPcapWithoutAdapterIsRefused covers the one combination of the two network flags
// that cannot produce a machine. QEMU refuses it too, but only after it has started
// building one, and it names the netdev id this command invented rather than the flag
// the user set.
func TestPcapWithoutAdapterIsRefused(t *testing.T) {
	err := runFlagError(t, "--nic", "none", "--pcap", "/tmp/x.pcap")
	// The message has to name both flags: which of the two to change is the whole
	// question, and neither answer is obviously right.
	for _, want := range []string{"--pcap", "--nic none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// TestEmptyNICIsRefused covers --nic with nothing after it, which is how an unset
// variable arrives (--nic=$MODEL). It must not be read as a way of asking for no
// adapter: that would hand back a machine the caller did not ask for, and would make
// the --pcap refusal above name a flag they never typed.
// The message is matched rather than the mere presence of an error, and rather than
// the substring "--nic": the --pcap refusal names "--nic none" too, so a looser check
// would pass on the wrong refusal in the second case below and hold whether or not
// the empty model is caught at all.
func TestEmptyNICIsRefused(t *testing.T) {
	const want = "names no adapter model"
	for _, args := range [][]string{
		{"--nic", ""},
		// Refused on its own account, not by falling into the --pcap check — which
		// would report a --nic none the caller never wrote.
		{"--nic", "", "--pcap", "/tmp/x.pcap"},
	} {
		err := runFlagError(t, args...)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("run %v refused with %q, want it to name %q", args, err, want)
		}
	}
}

// TestNICIsPassedThroughToTheImageLookup guards the refusals above against becoming
// over-broad: an ordinary model must get past them and fail on the missing image
// instead. Without this, a check that refused everything would pass every test here.
func TestNICIsPassedThroughToTheImageLookup(t *testing.T) {
	err := runFlagError(t, "--nic", "rtl8139", "--pcap", "/tmp/x.pcap")
	if strings.Contains(err.Error(), "--nic") || strings.Contains(err.Error(), "--pcap") {
		t.Errorf("error %q rejected a usable pair of flags", err)
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

	none := write("none.hcl", src+"output {\n  target = \"none\"\n}\n")
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

// TestImageArchClassicLayout covers the layout that always worked: a built tree whose
// architecture folder sits at the image root. amd64 is the case worth pinning — x64
// media carries an i386 folder too (the real-mode boot chain), so a detector that
// stopped at the first folder it found would call an x64 image i386 and boot it on a
// 32-bit CPU.
func TestImageArchClassicLayout(t *testing.T) {
	for _, want := range []string{source.ArchI386, source.ArchAMD64} {
		t.Run(want, func(t *testing.T) {
			dir := t.TempDir()
			writeTreeFile(t, dir, want+"/system32/ntoskrnl.exe", "KERNEL")
			if want == source.ArchAMD64 {
				writeTreeFile(t, dir, "i386/ntdetect.com", "BOOT")
			}
			got, err := imageArch(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("imageArch = %q, want %q", got, want)
			}
		})
	}
}

// TestImageArchWIMLayout is the gap this covers: a wim-layout image carries no
// architecture folder on its media root — the tree is sealed inside PE.wim — so the
// architecture is only visible by reading through the payload. Without that, run
// refused an image build had just written.
func TestImageArchWIMLayout(t *testing.T) {
	tree := t.TempDir()
	writeTreeFile(t, tree, "i386/system32/ntoskrnl.exe", "KERNEL")

	dir := t.TempDir()
	if err := wim.CaptureDir(context.Background(), tree, filepath.Join(dir, wimImageFile),
		wim.ImageInfo{Name: "PE", Boot: true},
		wim.Options{Security: wim.UniformSecurity(minimalSecurityDescriptor())}); err != nil {
		t.Fatalf("CaptureDir: %v", err)
	}
	// The media-root files a wim-layout image carries beside the payload. None of them
	// names an architecture, which is the point.
	writeTreeFile(t, dir, "grldr", "loader")
	writeTreeFile(t, dir, "coreimg.gz", "gz")
	writeTreeFile(t, dir, "menu.lst", "menu")

	got, err := imageArch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != source.ArchI386 {
		t.Errorf("imageArch = %q, want %q", got, source.ArchI386)
	}
}

// A wim-layout image whose payload cannot be read must be reported as that, not as an
// image with no architecture folder: the folder is missing because the reader failed,
// and the old message blamed the image for it.
func TestImageArchUnreadableWIM(t *testing.T) {
	dir := t.TempDir()
	writeTreeFile(t, dir, wimImageFile, "not a WIM at all")
	writeTreeFile(t, dir, "grldr", "loader")

	_, err := imageArch(dir)
	if err == nil {
		t.Fatal("imageArch on an unreadable payload returned no error")
	}
	if !strings.Contains(err.Error(), wimImageFile) {
		t.Errorf("error = %v, want it to name %s", err, wimImageFile)
	}
}

// An image that is neither layout keeps the message it had: it is not a built PE.
func TestImageArchNotAnImage(t *testing.T) {
	dir := t.TempDir()
	writeTreeFile(t, dir, "readme.txt", "nothing to boot")

	_, err := imageArch(dir)
	if err == nil {
		t.Fatal("imageArch on a directory that is not an image returned no error")
	}
	if !strings.Contains(err.Error(), "not a built PE image") {
		t.Errorf("error = %v, want it to say the directory is not a built PE image", err)
	}
}
