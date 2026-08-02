// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/source"
)

// defaultRAM is the guest memory size in MiB. It is the value the documented
// quick-start machine uses, comfortably above the 128 MiB a desktop image needs.
const defaultRAM = 512

// kvmDevice is the host interface whose presence decides hardware acceleration.
const kvmDevice = "/dev/kvm"

// qemuBinaries lists the QEMU binaries that can boot an image of each architecture,
// in preference order. The binary follows the *image's* architecture rather than the
// host's: qemu-system-i386 presents a 32-bit CPU, which an x64 Windows will not boot
// on, so an amd64 image has exactly one candidate. A 32-bit image runs equally well
// under either, so qemu-system-x86_64 is a fallback for a host that ships only it.
var qemuBinaries = map[string][]string{
	source.ArchAMD64: {"qemu-system-x86_64"},
	source.ArchI386:  {"qemu-system-i386", "qemu-system-x86_64"},
}

// machine is everything needed to build a QEMU command line for one image.
type machine struct {
	// Binary is the QEMU executable to run.
	Binary string
	// ISO is the image to boot from.
	ISO string
	// Arch is the image's architecture, as source.DetectArch reports it.
	Arch string
	// RAM is the guest memory size in MiB.
	RAM int
	// KVM enables hardware acceleration.
	KVM bool
	// USBTablet adds a USB tablet, whose absolute pointing lets the host cursor
	// cross into the guest without a grab. It needs a guest driver to be useful,
	// so it is opt-in rather than a default that silently does nothing.
	USBTablet bool
	// Serial is a QEMU -serial specification, e.g. "unix:/tmp/kd.sock,server,nowait".
	// It exists so an image built with the serialdebug plugin can be booted with this
	// command rather than a hand-written QEMU line: that plugin turns the kernel
	// debugger on, and with no debugger attached the kernel suppresses DbgPrint and a
	// bugcheck waits silently, so a debug image booted without a serial port looks
	// frozen rather than debuggable. Empty means QEMU's default (no serial port).
	Serial string
}

func newRunCmd() *cobra.Command {
	var ram int
	var usbTablet bool
	var serial string

	cmd := &cobra.Command{
		Use:   "run [manifest.hcl | image.iso]",
		Short: "Boot a built image in QEMU",
		Long: "Boot an already-built image in QEMU, picking the emulator and machine to\n" +
			"match it. The argument is either a built .iso, or the manifest that\n" +
			"describes where one was written; with no argument, pebuild.hcl in the\n" +
			"current directory is used.\n\n" +
			"This command does not build. Run pebuild build first, then boot the result.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// QEMU accepts -m 0 and starts a machine that cannot boot, so a
			// nonsensical size has to be refused here to be refused at all.
			if ram < 1 {
				return fmt.Errorf("--ram %d is not a usable memory size", ram)
			}
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			iso, err := resolveImage(arg)
			if err != nil {
				return err
			}

			arch, err := imageArch(iso)
			if err != nil {
				return err
			}
			binary, err := findQEMU(arch)
			if err != nil {
				return err
			}

			kvm, notice := kvmStatus()
			if notice != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", notice)
			}
			if !kvm {
				slog.Debug("no hardware acceleration", "device", kvmDevice)
			}

			return launch(cmd.OutOrStdout(), machine{
				Binary:    binary,
				ISO:       iso,
				Arch:      arch,
				RAM:       ram,
				KVM:       kvm,
				USBTablet: usbTablet,
				Serial:    serial,
			})
		},
	}
	cmd.Flags().IntVar(&ram, "ram", defaultRAM,
		"guest memory size in MiB (128 or more for a desktop image)")
	cmd.Flags().BoolVar(&usbTablet, "usb-tablet", false,
		"add a USB tablet for seamless mouse (needs the guest usb-input plugin)")
	cmd.Flags().StringVar(&serial, "serial", "",
		"attach COM1 to a QEMU chardev, e.g. unix:/tmp/kd.sock,server,nowait "+
			"(for an image built with the serialdebug plugin)")
	return cmd
}

// resolveImage returns the image to boot for a command argument, which is either an
// image itself or a manifest naming where one was written.
func resolveImage(arg string) (string, error) {
	// Only an explicit argument can be an image; the implied argument is the
	// default manifest, and there is no default image to fall back to.
	if arg != "" {
		image, err := isImage(arg)
		if err != nil {
			return "", err
		}
		if image {
			return arg, nil
		}
	}

	m, path, err := loadManifest(arg)
	if err != nil {
		return "", err
	}
	if m.Output.Target == manifest.TargetNone {
		return "", fmt.Errorf("%s builds no media (output.target = %q), so there is nothing to boot",
			path, manifest.TargetNone)
	}

	// output.path is resolved against the current directory, not the manifest's,
	// which is what build does with it — the two have to agree, or run would look
	// somewhere build never writes. Report the resolved path rather than the
	// manifest's own text: with a manifest from elsewhere, a bare "./out/pe.iso"
	// gives no hint that it was read relative to where the command was run.
	iso := m.Output.Path
	abs, err := filepath.Abs(iso)
	if err != nil {
		abs = iso
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("no image at %s (output.path %q in %s, resolved against the current directory); build it first with pebuild build %s",
			abs, iso, path, filepath.Clean(path))
	}
	return abs, nil
}

// isoDescriptor is the ISO9660 volume-descriptor signature and where it sits: the
// standard identifier of the first descriptor, which begins at the start of logical
// sector 16 (2048-byte sectors), one byte in past the descriptor's type.
const (
	isoDescriptorID     = "CD001"
	isoDescriptorOffset = 16*2048 + 1
)

// isImage reports whether an explicit argument is a built image rather than a
// manifest, deciding on the file's own bytes: an image carries an ISO9660 volume
// descriptor. Content decides rather than the extension — matching how a hive target
// is recognised by its regf magic — so a manifest saved as pe.iso, or an image with
// any other extension, is read as what it actually is.
//
// An argument that is neither is an error here rather than a false: a path that does
// not exist, a directory, or a file that cannot be read would otherwise fall through
// to the manifest reader and be reported as broken manifest syntax, blaming a
// mistyped image path on the wrong thing entirely.
func isImage(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory; give a built image or the manifest that wrote one", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Too short to hold a descriptor is not an image, and not an error: a manifest
	// is a small file, so this is the ordinary way a manifest answers no.
	var id [len(isoDescriptorID)]byte
	if _, err := f.ReadAt(id[:], isoDescriptorOffset); err != nil {
		return false, nil
	}
	return string(id[:]) == isoDescriptorID, nil
}

// imageArch reports the architecture of a built image, which decides the emulator.
//
// A built PE cannot be probed the way a source can — the files Probe reads a version
// from are decompressed and moved during the build — so the architecture comes from
// the folder layout the build preserved.
func imageArch(iso string) (string, error) {
	src, err := source.Open(iso)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", iso, err)
	}
	arch, ok := source.DetectArch(src.Backing())
	if !ok {
		return "", fmt.Errorf("%s has no %s folder, so it is not a built PE image",
			iso, strings.Join(source.ArchFolders(), " or "))
	}
	return arch, nil
}

// findQEMU returns the first QEMU binary on PATH that can boot the architecture.
func findQEMU(arch string) (string, error) {
	candidates := qemuBinaries[arch]
	if len(candidates) == 0 {
		return "", fmt.Errorf("no QEMU binary known for a %s image", arch)
	}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("booting an %s image needs %s on PATH; install QEMU (e.g. apt install qemu-system-x86)",
		arch, strings.Join(candidates, " or "))
}

// kvmStatus reports whether the host can accelerate the guest, and a notice to show
// the user when it cannot but plainly could. It opens the device read-write rather
// than stating it, because membership in the group that grants that access is the
// half of the question a stat cannot answer.
//
// Either way this is a fall back to emulation rather than a failure to run, but the
// two cases deserve different volume. No /dev/kvm at all is normal — a container, a
// CI runner, a foreign host architecture — and says nothing worth interrupting for.
// A device that exists and will not open is a fixable permission problem, and
// silence there costs the user an order of magnitude in speed while looking like the
// tool is simply slow.
func kvmStatus() (kvm bool, notice string) { return kvmStatusAt(kvmDevice) }

// kvmStatusAt is kvmStatus against a given device, so the permission case can be
// tested on a host where /dev/kvm opens perfectly well.
func kvmStatusAt(device string) (kvm bool, notice string) {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return true, ""
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, ""
	}
	return false, fmt.Sprintf("%s exists but could not be opened (%v); booting without hardware "+
		"acceleration, which is much slower. Adding your user to the kvm group usually fixes this.",
		device, err)
}

// args renders the QEMU command line for the machine. It is separate from launching
// so the machine a set of flags produces can be checked without running QEMU.
func (m machine) args() []string {
	args := []string{"-M", "pc", "-m", strconv.Itoa(m.RAM), "-boot", "d", "-cdrom", m.ISO}
	if m.KVM {
		args = append(args, "-enable-kvm")
	} else if m.Arch == source.ArchAMD64 {
		// Without KVM the guest gets QEMU's default 32-bit CPU model, which
		// advertises no long mode, and an x64 Windows stops at the boot loader.
		args = append(args, "-cpu", "qemu64")
	}
	if m.USBTablet {
		args = append(args, "-device", "usb-tablet")
	}
	if m.Serial != "" {
		args = append(args, "-serial", m.Serial)
	}
	return args
}

// launch runs QEMU and waits for it to exit.
//
// QEMU inherits this process's streams and process group, so it shares the
// terminal's Ctrl-C and its exit status is this command's: closing the guest window
// ends the run normally, and a QEMU that fails to start says why in its own words.
func launch(w io.Writer, m machine) error {
	args := m.args()
	fmt.Fprintf(w, "Booting %s (%s) in %s\n", m.ISO, m.Arch, filepath.Base(m.Binary))
	slog.Debug("qemu", "binary", m.Binary, "args", args, "kvm", m.KVM)

	cmd := exec.Command(m.Binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(m.Binary), err)
	}
	return nil
}
