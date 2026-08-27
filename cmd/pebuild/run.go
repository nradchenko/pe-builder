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

	"github.com/nradchenko/pe-builder/internal/disk"
	"github.com/nradchenko/pe-builder/internal/manifest"
	"github.com/nradchenko/pe-builder/source"
)

// defaultRAM is the guest memory size in MiB. It is the value the documented
// quick-start machine uses, comfortably above the 128 MiB a desktop image needs.
const defaultRAM = 512

// kvmDevice is the host interface whose presence decides hardware acceleration.
const kvmDevice = "/dev/kvm"

// usbBusID names the USB controller the tablet is attached to. QEMU's id namespace
// is per-machine and this command builds the whole command line, so a fixed id
// cannot collide with anything.
const usbBusID = "ehci"

// netdevID names the host network backend the adapter is attached to, and the one the
// packet capture taps. Fixed for the same reason as usbBusID.
const netdevID = "net0"

// pcapID names the capture filter object. QEMU requires an id on -object.
const pcapID = "pcap0"

// defaultNIC is the adapter model presented to the guest.
//
// RTL8139 rather than QEMU's own default for the "pc" machine, which is e1000: no
// supported medium carries an INF claiming e1000's PCI IDs, so that adapter installs
// no driver and an image with the hardware plugin looks like it cannot configure a
// network. RTL8139 is the one model every supported source has a driver for on both
// architectures — on amd64 media it is the only one at all — which is also why it is
// what the network support is verified against. See plugins/hardware/hardware.inf.
const defaultNIC = "rtl8139"

// nicNone is the --nic value that offers the guest no adapter. It is spelled as the
// state it selects rather than as a negated flag, and it also has to be said to QEMU:
// left to itself QEMU gives the "pc" machine an adapter whether one is wanted or not.
const nicNone = "none"

// What the "pc" machine's IDE controller can carry, and where the CD sits on it.
//
// The controller has two buses of two units each, so indices 0 to 3 and nothing beyond:
// index 4 is refused with "machine type does not support if=ide,bus=2,unit=0". The CD is
// not outside that budget — `-cdrom` is itself shorthand for a drive at index 2 on the
// same default interface — so it occupies one of the four, and an attached disk numbered
// straight from zero collides with it on the third disk, QEMU refusing to start at all
// with "drive with bus=1, unit=0 (index=2) exists".
//
// Both facts measured against qemu-system-i386 on -M pc rather than taken from the flag's
// documentation, which describes -cdrom as its own option and does not say what it costs.
const (
	cdromIndex   = 2
	maxDiskCount = 3
)

// driveIndex maps the nth attached disk to the IDE slot it takes, stepping over the one
// the CD already holds.
func driveIndex(n int) int {
	if n < cdromIndex {
		return n
	}
	return n + 1
}

// qemuBinaries lists the QEMU binaries that can boot an image of each architecture,
// in preference order. The binary follows the *image's* architecture rather than the
// host's: qemu-system-i386 presents a 32-bit CPU, which an x64 Windows will not boot
// on, so an amd64 image has exactly one candidate. A 32-bit image runs equally well
// under either, so qemu-system-x86_64 is a fallback for a host that ships only it.
var qemuBinaries = map[string][]string{
	source.ArchAMD64: {"qemu-system-x86_64"},
	source.ArchI386:  {"qemu-system-i386", "qemu-system-x86_64"},
}

// Drive is one disk attached to the guest.
type Drive struct {
	// Path is the image file on the host.
	Path string
	// Format is the QEMU image format, from disk.SniffFormat — stated rather than left
	// for QEMU to probe, which warns about probed raw images and has changed across
	// versions.
	Format string
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
	// USBTablet adds a USB tablet, which is the default pointer. Its absolute
	// positions are what let the cursor cross between host and guest window with
	// no grab.
	//
	// Every image drives it, with no plugin: the source carries its USB HID stack
	// in the text-setup boot configuration so USB keyboards work during setup, and
	// the base layer loads those classes, so the chain comes up on a bare cmd.exe
	// image as much as on a desktop one.
	//
	// The machine's PS/2 mouse belongs to the built-in i8042 controller and cannot
	// be removed, so the tablet is added beside it and QEMU routes pointer input to
	// whichever the guest has enabled, preferring the absolute one.
	USBTablet bool
	// Disks are the drives to attach, in the order they will be indexed: every --disk in
	// the order given, then every --scratch-disk in the order given.
	//
	// That is a stated rule rather than the order they appeared on the command line,
	// which cannot be recovered: the two flags parse into separate slices, so an
	// interleaved command line arrives here already separated. Better to assert a rule
	// the implementation keeps than to imply one it cannot.
	//
	// The CD keeps the boot either way (-boot d), so an attached disk carrying an active
	// partition cannot take it.
	Disks []Drive
	// Serial is a QEMU -serial specification, e.g. "unix:/tmp/kd.sock,server,nowait".
	// It exists so an image built with the serialdebug plugin can be booted with this
	// command rather than a hand-written QEMU line: that plugin turns the kernel
	// debugger on, and with no debugger attached the kernel suppresses DbgPrint and a
	// bugcheck waits silently, so a debug image booted without a serial port looks
	// frozen rather than debuggable. Empty means QEMU's default (no serial port).
	Serial string
	// NIC is the network adapter model to present, on a user-mode backend that needs
	// no host privilege. Empty offers the guest no adapter at all.
	NIC string
	// Pcap is a file to write the adapter's traffic to, in pcap format. It taps the
	// host end of the backend, so it records what the guest actually put on the wire
	// — which is the only way to tell a stack that never transmits from one whose
	// replies never arrive. Empty captures nothing.
	Pcap string
}

func newRunCmd() *cobra.Command {
	var ram int
	var ps2Mouse bool
	var serial string
	var nic string
	var pcap string
	var disks, scratch []string

	cmd := &cobra.Command{
		Use:   "run [manifest.hcl | image.iso]",
		Short: "Boot a built image in QEMU",
		Long: "Boot an already-built image in QEMU, picking the emulator and machine to\n" +
			"match it. The argument is either a built .iso, or the manifest that\n" +
			"describes where one was written; with no argument, pebuild.hcl in the\n" +
			"current directory is used.\n\n" +
			"This command does not build. Run pebuild build first, then boot the result.\n\n" +
			"Disks can be attached, which is what any question about drive letters or the\n" +
			"storage stack needs. --disk attaches an image you already have; --scratch-disk\n" +
			"creates a formatted one beside the image and attaches that, reusing it on\n" +
			"later runs so what the guest wrote survives — read it back with pebuild disk.\n" +
			"The size is the whole disk, and the volume inside it is a megabyte smaller.\n\n" +
			"Both flags repeat. Disks are indexed in the order given, every --disk before\n" +
			"every --scratch-disk. The CD always keeps the boot.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// QEMU accepts -m 0 and starts a machine that cannot boot, so a
			// nonsensical size has to be refused here to be refused at all.
			if ram < 1 {
				return fmt.Errorf("--ram %d is not a usable memory size", ram)
			}
			// An empty model is a mistake rather than a way of asking for no
			// adapter — --nic=$MODEL with the variable unset is how it arrives —
			// so it is refused by the flag it came from. Left to mean "none" it
			// would quietly produce a machine the caller did not ask for, and
			// answer a later complaint about --pcap by naming a flag they never
			// typed.
			if nic == "" {
				return fmt.Errorf("--nic names no adapter model; use --nic %s to offer the guest none", nicNone)
			}
			// A capture taps the adapter's backend, so with no adapter there is
			// nothing to tap. QEMU would refuse the filter itself, but only after
			// starting to build a machine, and its message names the netdev id this
			// command invented rather than the flag the user set.
			//
			// The test is the model this resolves to, not the word the caller
			// wrote: it is the same question args() asks when it decides whether
			// there is an adapter to attach the filter to.
			if pcap != "" && nicModel(nic) == "" {
				return fmt.Errorf("--pcap captures the traffic of an adapter, and --nic %s offers none", nicNone)
			}
			// Sizes are parsed before the image is resolved so a typo fails on its own
			// terms rather than after a manifest lookup that had nothing to do with it.
			sizes := make([]int64, 0, len(scratch))
			for _, s := range scratch {
				n, err := disk.ParseSize(s)
				if err != nil {
					return fmt.Errorf("--scratch-disk %s: %w", s, err)
				}
				sizes = append(sizes, n)
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

			drives, err := resolveDrives(cmd.OutOrStdout(), cmd.ErrOrStderr(), iso, disks, sizes)
			if err != nil {
				return err
			}

			return launch(cmd.OutOrStdout(), machine{
				Binary:    binary,
				ISO:       iso,
				Arch:      arch,
				RAM:       ram,
				KVM:       kvm,
				USBTablet: !ps2Mouse,
				Serial:    serial,
				NIC:       nicModel(nic),
				Pcap:      pcap,
				Disks:     drives,
			})
		},
	}
	cmd.Flags().IntVar(&ram, "ram", defaultRAM,
		"guest memory size in MiB (128 or more for a desktop image)")
	cmd.Flags().BoolVar(&ps2Mouse, "ps2-mouse", false,
		"offer no USB tablet, leaving the guest the machine's PS/2 mouse "+
			"(the cursor is then grabbed)")
	cmd.Flags().StringVar(&serial, "serial", "",
		"attach COM1 to a QEMU chardev, e.g. unix:/tmp/kd.sock,server,nowait "+
			"(for an image built with the serialdebug plugin)")
	cmd.Flags().StringVar(&nic, "nic", defaultNIC,
		"network adapter model to present, on user-mode networking; "+nicNone+" for no adapter")
	cmd.Flags().StringVar(&pcap, "pcap", "",
		"write the adapter's traffic to a file, in pcap format")
	// StringArray rather than StringSlice: the latter splits its value on commas, which
	// would quietly turn one path containing a comma into two paths that do not exist.
	cmd.Flags().StringArrayVar(&disks, "disk", nil,
		"attach an existing disk image, repeatable (see also --scratch-disk)")
	cmd.Flags().StringArrayVar(&scratch, "scratch-disk", nil,
		"create and attach a formatted disk of this size beside the image, e.g. 256M, "+
			"repeatable; an existing one is reused, never rewritten")
	return cmd
}

// nicModel turns the --nic value into the machine's adapter model, mapping the
// spelled-out "none" onto the empty model that offers the guest no adapter.
func nicModel(flag string) string {
	if flag == nicNone {
		return ""
	}
	return flag
}

// resolveDrives settles the disks to attach and reports each one.
//
// Attached disks come first and scratch disks after, which is the rule machine.Disks
// documents — the two flags arrive in separate slices, so the command line's own
// interleaving is not available to honour.
func resolveDrives(out, errOut io.Writer, iso string, disks []string, sizes []int64) ([]Drive, error) {
	if n := len(disks) + len(sizes); n > maxDiskCount {
		// Refused here rather than left to QEMU, whose message names an IDE bus and unit
		// the caller never mentioned. The limit is the machine's, not ours.
		return nil, fmt.Errorf("%d disks were given; the machine's IDE controller carries at most %d beside the CD",
			n, maxDiskCount)
	}

	drives := make([]Drive, 0, len(disks)+len(sizes))
	// verbs runs alongside drives, saying how each came to be attached. It is reporting
	// only, which is why it does not live on Drive: that type describes the machine.
	verbs := make([]string, 0, len(disks)+len(sizes))
	// Attaching one host file twice gives the guest two independent writable views of it,
	// which corrupts the volume as surely as two machines sharing a disk would. The paths
	// are compared resolved, since the same file reached by two spellings is still one
	// file — and the natural way to hit this is to name the scratch disk with --disk as
	// well, where the two spellings differ by construction.
	seen := make(map[string]string, len(disks)+len(sizes))
	claim := func(flag, p string) error {
		key, err := filepath.Abs(p)
		if err != nil {
			key = p
		}
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("%s %s is the same file as %s; attaching one disk twice lets the guest corrupt it",
				flag, p, prev)
		}
		seen[key] = p
		return nil
	}

	for _, p := range disks {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("--disk %s: %w", p, err)
		}
		if err := claim("--disk", p); err != nil {
			return nil, err
		}
		format, err := disk.SniffFormat(p)
		if err != nil {
			return nil, fmt.Errorf("--disk %s: %w", p, err)
		}
		drives = append(drives, Drive{Path: p, Format: format})
		verbs = append(verbs, "attached")
	}

	for i, size := range sizes {
		p := scratchPath(iso, i)
		if err := claim("--scratch-disk", p); err != nil {
			return nil, err
		}
		created, err := ensureScratch(errOut, p, size)
		if err != nil {
			return nil, err
		}
		drives = append(drives, Drive{Path: p, Format: disk.FormatRaw})
		if created {
			verbs = append(verbs, "created")
		} else {
			// Worth saying: a reused disk still holds whatever the last boot left on it,
			// which is either the point or a surprise, and either way should not be
			// something the user has to remember.
			verbs = append(verbs, "reusing")
		}
	}

	// Printed after everything resolved, so a failure part way leaves no half-report
	// claiming disks that were never attached.
	for i, d := range drives {
		describeDrive(out, i, d, verbs[i])
	}
	return drives, nil
}

// ensureScratch creates the scratch disk at a path if it is absent, and otherwise reuses
// what is there — a disk is a lasting artifact that may hold whatever a guest last wrote,
// so this never rewrites one.
//
// A size that disagrees with the disk that exists is reported and ignored rather than
// acted on: refusing would turn a flag typo into a failed boot on a command whose job is
// to boot quickly, and re-creating would destroy the data the persistence exists to keep.
func ensureScratch(errOut io.Writer, path string, size int64) (created bool, err error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			return false, statErr
		}
		if err := disk.Create(path, disk.Spec{Size: size}); err != nil {
			return false, err
		}
		return true, nil
	}

	img, err := disk.OpenRead(path)
	if err != nil {
		// Something else owns this name. Saying so beats silently replacing it, and
		// beats a message about the boot that mentions no disk at all.
		return false, fmt.Errorf("%s exists but is not a disk this tool can read: %w", path, err)
	}
	defer img.Close()

	if got := img.Info().DiskSize; got != size {
		fmt.Fprintf(errOut, "warning: %s already exists at %s, so the requested %s was ignored\n",
			path, disk.FormatSize(got), disk.FormatSize(size))
	}
	return false, nil
}

// scratchPath derives where the nth scratch disk for an image lives: beside the image,
// named after it. The image sits where the user can find it, so its disk should too — the
// same reasoning that kept the built ISO out of a cache directory.
func scratchPath(iso string, n int) string {
	base := strings.TrimSuffix(iso, filepath.Ext(iso))
	if n == 0 {
		return base + "-scratch.img"
	}
	return fmt.Sprintf("%s-scratch%d.img", base, n+1)
}

// describeDrive reports one attached disk, numbered as the guest will see it.
//
// Every disk is described by what it actually holds rather than by what was asked for —
// the volume is smaller than the disk, and a scratch disk's filesystem may have been
// chosen rather than given. That applies equally to a disk pebuild did not make: if it
// can be read, saying what is on it is more use than repeating the path. The format-only
// line is the fallback for a disk that cannot be opened, which is the ordinary case for a
// qcow2 or a filesystem this tool does not read, and not a failure worth interrupting a
// boot for.
func describeDrive(w io.Writer, index int, d Drive, verb string) {
	img, err := disk.OpenRead(d.Path)
	if err != nil {
		fmt.Fprintf(w, "Disk %d: %s (%s, %s)\n", index, d.Path, verb, d.Format)
		return
	}
	defer img.Close()
	info := img.Info()
	fmt.Fprintf(w, "Disk %d: %s (%s, %s, %s %q)\n",
		index, d.Path, verb, disk.FormatSize(info.VolumeSize), info.FS, info.Label)
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
//
// The image is opened the way the inspection commands open one, which is what makes
// this work for either layout: a wim-layout image keeps its tree inside PE.wim and its
// media root carries no architecture folder at all, so the folder is only visible
// through the payload openImage merges over that root. Reading the image the same way
// `image ls` does also keeps the two commands from disagreeing about what a built image
// is, which is how a wim image ended up bootable by hand but rejected here.
func imageArch(iso string) (string, error) {
	b, err := openImage(iso)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", iso, err)
	}
	arch, ok := source.DetectArch(b)
	if !ok {
		// A wim-layout image whose payload could not be read has no folder to find,
		// and saying it has none would blame the image for what is a reader failure.
		if wimPayloadOpaque(b) {
			return "", fmt.Errorf("%s is a WIM-layout image whose payload (%s) could not be read, so the emulator to boot it with is unknown",
				iso, wimImageFile)
		}
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
		// The tablet needs a controller to plug into, and the "pc" machine has no
		// USB bus at all, so a bare -device usb-tablet is refused outright ("No
		// 'usb-bus' bus found"). The controller is therefore added with the device
		// and the device attached to it by name rather than left to a default bus
		// that does not exist.
		args = append(args, "-device", "usb-ehci,id="+usbBusID,
			"-device", "usb-tablet,bus="+usbBusID+".0")
	}
	if m.NIC != "" {
		// The backend is user-mode networking: it needs no host privilege and no
		// bridge, and gives the guest a DHCP server, which is what an image with the
		// hardware plugin is waiting for.
		args = append(args, "-device", m.NIC+",netdev="+netdevID,
			"-netdev", "user,id="+netdevID)
		if m.Pcap != "" {
			args = append(args, "-object",
				"filter-dump,id="+pcapID+",netdev="+netdevID+",file="+m.Pcap)
		}
	} else {
		args = append(args, "-nic", nicNone)
	}
	if m.Serial != "" {
		args = append(args, "-serial", m.Serial)
	}
	// Appended after everything above rather than woven into it, so the boot line a
	// diskless run produces is byte-for-byte what it was before disks existed. Position
	// in Disks is the order the guest sees them in and the order the status lines number
	// them by; driveIndex turns that into the IDE slot, which is not the same number once
	// the CD's slot is stepped over.
	for i, d := range m.Disks {
		args = append(args, "-drive",
			fmt.Sprintf("file=%s,format=%s,index=%d,media=disk", d.Path, d.Format, driveIndex(i)))
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
