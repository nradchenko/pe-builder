# Supported sources and environments

What pe-builder can build from, and where the result is known to run. "Verified" means a
build was made from that source and booted; anything not verified is marked as such rather
than assumed to work.

## Windows sources

You supply the installation source; pe-builder ships no Microsoft files. A source is a
mounted directory or an `.iso`.

| Source | Builds | Boots to `cmd.exe` | Explorer desktop | Luna theme | Wallpaper |
|---|---|---|---|---|---|
| **Windows XP SP3 (32-bit)** | yes | verified | verified | verified | verified |
| **Windows XP Professional x64 Edition (SP2)** | yes | verified | verified | verified | no — see below |
| Windows Server 2003 (32-bit) | accepted | untested | untested | untested | untested |
| Windows Server 2003 (x64) | accepted | untested | untested | untested | untested |
| Windows XP SP0 / SP1 / SP2 | **rejected** | — | — | — | — |
| Windows 2000 and earlier | **rejected** | — | — | — | — |
| Windows Fundamentals for Legacy PCs | **out of scope** | — | — | — | — |

The 32-bit XP path is verified against both English and Russian volume-licence media, which
behave identically apart from one Active Setup component present only on some locales.

**Service-pack floor.** A source must be at least XP SP3 (`5.1.2600.5512`, the revision SP3
media ships `ntdll` at); anything older is refused when the source is probed. SP3 was the
last and universal XP service pack and supersedes the earlier ones, so building from SP0–SP2
media has almost no practical use and is not a target.

**Server 2003 is accepted but unproven.** It is newer than the floor, so nothing rejects it
and it may well build — but no image has been verified from it.

**Windows Fundamentals for Legacy PCs is out of scope**, despite being XP-based. Its OS ships
as a Windows Imaging (`.wim`) image under `Setup\WIM`, and its `I386` holds a WinPE boot
environment for the deployment tool rather than the file payload a build reads. Supporting it
would mean a WIM reader and a second, image-based source model.

### What differs on an x64 build

An amd64 source produces the same themed desktop a 32-bit one does. Plugins that ship code
are compiled for the target architecture, so building one needs the 64-bit cross-compiler
(`gcc-mingw-w64-x86-64`) rather than the 32-bit one.

Three known gaps, all amd64-only and all working on a 32-bit build:

- **Folder Options and Taskbar properties do nothing.** Both open through the same shell
  entry point, which posts a message to the shell window; Explorer receives it and does not
  act on it. Everything upstream is verified — the shell window is registered, the message is
  delivered, and Explorer has the v6 Common-Controls assembly loaded — so the fault is inside
  Explorer's own handling of the message. The rest of Control Panel is unaffected.
- **A drive's Properties dialog does not work.** Right-clicking a drive and choosing
  **Properties** — the General/Hardware sheet that opens normally on a 32-bit build — does not
  work on amd64. The cause is not yet established.
- **The `wallpaper` plugin does not render.** The desktop comes up on a flat colour instead
  of the image. The plugin builds and stages its files, and the wallpaper is configured in
  the registry, but the Active Desktop surface does not paint it on amd64. The cause is not
  yet established.

## Output

| Target | Status |
|---|---|
| Bootable ISO (El Torito, no-emulation) | supported |
| USB image | not implemented |

## Where the result runs

| Environment | Status |
|---|---|
| **QEMU** | verified — the environment every image is tested in, both architectures |
| Real hardware | **untested** — nothing here has been booted on a physical machine |
| Other hypervisors (VirtualBox, VMware, Hyper-V) | untested |

`pebuild run <manifest|image.iso>` boots a built image with the machine below, reading the
image to settle every choice this section describes — the binary, hardware acceleration,
and the CPU model. The commands are spelled out here because they document what it does,
and for driving QEMU yourself.

**The QEMU binary follows the image's architecture**, not the host's: a 32-bit image runs
under `qemu-system-i386`, and a 64-bit image needs `qemu-system-x86_64`, because
`qemu-system-i386` presents a 32-bit CPU that an x64 Windows will not boot on. Everything
else about the machine is the same for both.

```sh
# a 32-bit image
qemu-system-i386   -enable-kvm -M pc -m 512 -cdrom out/pe.iso -boot d

# a 64-bit image — same machine, 64-bit binary and CPU
qemu-system-x86_64 -enable-kvm -M pc -m 512 -cdrom out/pe-x64.iso -boot d
```

Use `-enable-kvm` where the host supports it. It is dramatically faster than emulation, and
it is the configuration timings in this project are quoted for. Without KVM, add `-cpu qemu64`
for a 64-bit image so the emulated CPU advertises long mode. (`run` uses `/dev/kvm` when it
can open it and falls back to emulation when it cannot, adding `-cpu qemu64` where that
fallback needs it. No `/dev/kvm` at all is quiet — a container or a CI runner has none — but
a device that is present and will not open is reported, since that is usually group
membership, and the difference otherwise shows up only as a much slower boot.)

### Guest requirements

The base layer targets the hardware an XP-era CD expects, which is what QEMU's default
machine presents. The two architectures differ only in the CPU:

| | 32-bit image | 64-bit image |
|---|---|---|
| Firmware / boot | BIOS, booting from CD | same |
| Chipset | i440FX (QEMU's `pc` machine) | same |
| CPU | any x86 | must support **long mode** (x86-64) |
| Display | standard VGA | same |
| Input | PS/2 keyboard and mouse | same |
| Storage | IDE/ATAPI CD-ROM | same |
| Memory | 128 MB minimum for a desktop image, less for `cmd.exe`; tested at 512 MB | tested at 512 MB |

`run` boots with 512 MB; `--ram <MB>` sets it, and `--usb-tablet` adds a USB tablet for
seamless mouse, which needs a guest that drives one.

No network, sound, or USB input stack is configured on either. UEFI is not supported — the
boot chain is XP's own real-mode loader, which is also why it is identical on both
architectures and why a 64-bit source still keeps that part of itself in `I386`.

A desktop image needs no attached disk: it carries a RAM-backed volume for its writable
profile.

## Known limitations

These apply to any build. The amd64-only gaps are under
[What differs on an x64 build](#what-differs-on-an-x64-build) above.

- **No sound.** No audio device is configured, and the Windows Audio service is deliberately
  disabled: with it enabled, a system sound blocks in `waveOut` on a service stuck in
  `START_PENDING`, which hangs the shell's tray thread and cascades every folder window into
  *Not Responding*. Anything that would play audio runs silently instead — the bundled games
  are playable but mute, and this is why Pinball is not among them.
- **Task Manager shows unreadable characters in its User Name column,** for every process
  except *System Idle Process*. The cause is not established; the leading suspicion is that a
  PE ships empty SAM and SECURITY hives (deliberately — see the note in `build/hives.go`), so
  there is no account database to resolve a process owner against. Cosmetic: only that column
  is affected, and the rest of Task Manager works.

## Build host

pe-builder is Linux-first and builds on any platform Go supports. Building an image whose
plugins ship as source — the desktop plugins do — additionally needs the MinGW-w64
cross-compiler for the target architecture. A bare `cmd.exe` image needs only Go.
