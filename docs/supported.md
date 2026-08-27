# Supported sources and environments

What pe-builder can build from, and where the result is known to run. "Verified" means a
build was made from that source and booted; anything not verified is marked as such rather
than assumed to work.

## Windows sources

You supply the installation source; pe-builder ships no Microsoft files. A source is a
mounted directory or an `.iso`.

| Source | Builds | Boots to `cmd.exe` | Explorer desktop | Luna theme | Wallpaper |
|---|---|---|---|---|---|
| **Windows XP Professional SP3 (32-bit)** | yes | verified | verified | verified | verified |
| **Windows XP Home Edition SP3 (32-bit)** | yes | verified | verified | verified | verified |
| **Windows XP Professional x64 Edition (SP2)** | yes | verified | verified | verified | no — [x64](#what-differs-on-an-x64-build) |
| **Windows Server 2003 R2 SP2 (32-bit)** | yes | verified | verified | verified | verified |
| **Windows Server 2003 R2 SP2 (x64)** | yes | verified | verified | verified | no — [x64](#what-differs-on-an-x64-build) |
| Windows XP SP0 / SP1 / SP2 | **rejected** | — | — | — | — |
| Windows 2000 and earlier | **rejected** | — | — | — | — |
| Windows Fundamentals for Legacy PCs | **out of scope** | — | — | — | — |

The 32-bit XP path is verified in several languages, which behave identically apart from one
Active Setup component present only on some locales. A build's language comes from the source;
see [Source language](#source-language) for which languages those are and what is verified in
each.

**Service-pack floor.** A source must be at least XP SP3 (`5.1.2600.5512`, the revision SP3
media ships `ntdll` at); anything older is refused when the source is probed. SP3 was the
last and universal XP service pack and supersedes the earlier ones, so building from SP0–SP2
media has almost no practical use and is not a target.

**Server 2003 reaches the themed Luna desktop on both architectures**, verified from R2 SP2
volume-licence media — the media ships `luna.msstyles` just as XP does. It shares a codebase
with XP x64: both are build 3790.

A build takes its product identity from the source, so a Server 2003 one reports itself as a
server: it boots under the server splash and names Server 2003 in System Properties, rather
than presenting as XP.

**Windows Fundamentals for Legacy PCs is out of scope**, despite being XP-based. Its OS ships
as a Windows Imaging (`.wim`) image under `Setup\WIM`, and its `I386` holds a WinPE boot
environment for the deployment tool rather than the file payload a build reads. Supporting it
would mean a WIM reader and a second, image-based source model.

### Source language

Any source builds and boots regardless of language, and reaches whatever desktop its edition
supports — that is the matrix above, not a language property. What language decides is whether
the source's **own script** appears: Cyrillic, Arabic, Japanese kana, Chinese characters. Latin
text always renders, because the base a build starts from is US-English. Where a script is not
configured, its characters come out as empty boxes:

```
活动的代码页: 936      <- configured
□□□□□□□: 936           <- not configured
```

| Source language | Its script in the console | Its script in windows |
|---|---|---|
| English (1252) | — Latin only | — Latin only |
| Russian (1251) | verified | verified |
| Arabic (1256, right-to-left) | verified — unjoined, see below | verified |
| Simplified Chinese (936, DBCS) | verified | verified |
| Japanese (932, DBCS) | not configured | not configured |
| Traditional Chinese (950, DBCS) | not configured | not configured |

Arabic is verified from XP Professional SP3 32-bit media. It renders its script in windows with
nothing staged for it — a single-byte locale needs no font of its own, because the UI fonts the
source already ships carry the script. Its console renders Arabic too, in Courier New, which the
source itself names as the fixed-pitch face for the script; the base's Lucida Console has no
Arabic glyphs.

**Console Arabic is unjoined.** The Windows console does no complex-script shaping, so it draws
isolated letterforms in logical order rather than the connected, right-to-left text the same
string gets in a window. That is how the console behaves on a full Windows install too, not
something the build introduces — but it does mean console Arabic is legible rather than
comfortable.

An Arabic build also comes out **mirrored** — Start button on the right, menus and dialogs
right-aligned. That is the right-to-left layout the source's own resources describe; the build
does nothing to arrange it.

Simplified Chinese is verified from XP SP3 and Server 2003 R2 SP2 volume-licence media, the
latter on both architectures. Japanese and Traditional Chinese build and boot, but no font or
console face has been configured for them, so their own script does not appear; nothing else
about them is known to be broken. A language not listed behaves the same way — its code pages
and keyboard layout come from the source automatically, so expect boxes until its fonts and
console face are added.

The two routes are configured separately, which is why one can work while the other shows
nothing but boxes:

- **Windowed text** resolves through the font links the source's own registry supplies, which
  name the locale's font (`SIMSUN.TTC` for Simplified Chinese). Staging that font is all this
  route needs.
- **The console** does not follow those links. It picks a face from the system's console-font
  list keyed by the code page, and nothing on the media populates that list or selects a face —
  Windows setup writes them in a locale pass a PE never runs, so the build supplies both.

A double-byte locale also loads the Text Services Framework input-context handler
(`msctfime.ime`) whenever a window takes input focus, which a single-byte one never does. It is
loaded by name, so no dependency scan finds it; the base layer seeds it for every source
because a build without it deadlocks the shell when a dialog returns focus.

A locale whose keyboard layout the build does not yet know still gets its own code pages and
console font; only the keyboard falls back to US-English. Simplified Chinese types through an
input method over the US layout and has no layout DLL of its own, so it uses that fallback by
design. **No input method is installed** — the handler above is input-context plumbing, not an
IME — so a Chinese build renders Chinese but cannot type it.

A service pack ships the resources it added to existing binaries in a small DLL of its own —
`xpsp2res.dll` on XP SP2 — which the shell, the network UI and others load by name. Localized
media store that DLL under a language-coded name (`sprt0419.dll` for Russian) and declare the
name it is installed as; a build follows that declaration, so those components find their
resources in the source's language.

Where several stored files declare the same installed name — Server 2003 does, one per MUI
language — a build does not pick one. A file reachable only that way is left out and listed
among the files the build skipped.

### What differs on an x64 build

An amd64 source produces the same themed desktop a 32-bit one does. Plugins that ship code
are compiled for the target architecture, so building one needs the 64-bit cross-compiler
(`gcc-mingw-w64-x86-64`) rather than the 32-bit one.

**The `wallpaper` plugin does not render**, on either x64 source: the desktop comes up on a flat
colour, and applying the wallpaper by hand afterwards does not paint it either. Staging and
registry are not the problem — the image is present, the COM classes register, and
`IActiveDesktop` accepts the wallpaper and reads it back — the Active Desktop surface is simply
never composed. The cause is not established. A 32-bit build is unaffected.

## Output

| Target | Status |
|---|---|
| Bootable ISO (El Torito, no-emulation) | supported |
| File tree only (`target = "none"`, no media) | supported |
| USB image | not implemented |

The `none` target stops after assembling the file tree and building the hives, for
inspecting what a manifest produces without mastering an image; see
[`manifest.md`](manifest.md).

### ISO layout

An ISO is assembled one of two ways, chosen by the manifest's `layout`:

| Layout | What it produces | Status |
|---|---|---|
| `classic` (default) | the file tree mastered directly onto the ISO | supported |
| `wim` | the tree packed into a compressed `.wim`, read on demand through an overlay filter — a smaller image | **experimental** — boots to the Luna desktop; verified on XP SP3 (32-bit), XP x64, and Server 2003 (both architectures) |

The `wim` layout requires an ISO target and three out-of-tree
plugins installed with `pebuild plugins add` — `wimmf` (the WIM overlay-filter driver),
`firadisk` (reclaims the RAM-mapped boot core as a disk), and `grub4dos` (the loader); the
built-in `wim-cd-boot` plugin that `layout = "wim"` enables wires them in. See the project
README and [`docs/wim-boot.md`](wim-boot.md) for the overview.

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
| Input | PS/2 keyboard and mouse, or USB HID | same |
| Storage | IDE/ATAPI CD-ROM | same |
| Memory | 128 MB minimum for a desktop image, less for `cmd.exe`; tested at 512 MB | tested at 512 MB |
| Power (ACPI) | an ACPI machine (QEMU's default) — the CPU idles only where an `ACPI\Processor` devnode exists; a non-ACPI *Standard PC* HAL leaves it spinning at 100% | same |

`run` boots with 512 MB and `--ram <MB>` sets it.

**The pointer is a USB tablet by default**, and **every image drives it — no plugin needed**.
XP carries its USB HID stack in the source's own text-setup boot configuration, in
`txtsetup.sif`'s `[InputDevicesSupport]` class, so that USB keyboards work during setup; the
base layer loads those classes, so the controller → hub → tablet → HID chain comes up even on
a bare `cmd.exe` image. Because a tablet reports absolute positions, **the cursor crosses
between host and guest window with no grab**, and the wheel works.

**Verified** on 32-bit XP SP3, on a bare base image and on an Explorer desktop built without
the `hardware` plugin, by QEMU reporting the tablet rather than the PS/2 mouse as the guest's
active pointer.

The PS/2 mouse is still present either way — it belongs to the machine's built-in i8042
controller and cannot be removed, so the tablet is added *beside* it and QEMU routes input to
whichever the guest has enabled, preferring the absolute one. `--ps2-mouse` offers no tablet
at all, for reproducing a plain XP-era machine or when the input stack is under investigation.

**Network.** With the `hardware` plugin enabled an image configures networking at boot and
takes a DHCP address; without it no network stack is configured at all.

| Source | Network |
|---|---|
| Windows XP Professional SP3 (32-bit) | **verified** — installs TCP/IP, binds it to the adapter, leases an address, answers `ping` |
| Windows XP Home Edition SP3 (32-bit) | **verified** — same |
| Windows Server 2003 R2 SP2 (32-bit) | **verified** — same |
| Windows Server 2003 R2 SP2 (x64) | **verified** — same |
| Windows XP Professional x64 Edition | **verified** — same |

**Verified** on an emulated **RTL8139** (`-device rtl8139`), which is the only adapter
tested. On amd64 media it is also the only adapter that *can* be tested: XP x64 ships no
INF for AMD PCnet, NE2000-PCI or e1000, so those emulated adapters have no driver to bind
whatever else works.

A bare console image starts fewer of the Windows services that sit above TCP/IP than a desktop
image does; none of them is needed for an address. See `plugins/hardware/README.md`.

No sound is configured on any build. UEFI is not supported — the
boot chain is XP's own real-mode loader, which is also why it is identical on both
architectures and why a 64-bit source still keeps that part of itself in `I386`.

A desktop image needs no attached disk: it carries a RAM-backed volume for its writable
profile.

## Known limitations

These apply to any build.

- **No sound.** No audio device is configured, and the Windows Audio service is deliberately
  disabled: with it enabled, a system sound blocks in `waveOut` on a service stuck in
  `START_PENDING`, which hangs the shell's tray thread and cascades every folder window into
  *Not Responding*. Anything that would play audio runs silently instead — the bundled games
  are playable but mute, and this is why Pinball is not among them.
- **My Documents has no desktop icon** — it is hidden deliberately, because the icon does not
  work: double-clicking it does nothing. **The folder itself is fine and still reachable from
  the Start menu**, which is why only the icon is removed.

  The split is the useful clue: the Start-menu entry opens a filesystem path, while the desktop
  icon is a namespace object (`{450D8FBA-AD25-11D0-98A8-0800361B1103}`) that must be
  instantiated through COM. So the profile directory and the `Personal` path are both good, and
  it is the shell folder object that does not come up. Consistent with the built `SOFTWARE`
  hive carrying no `Classes\CLSID` entry for it on any source — though that alone proves
  little, since peshell's self-registration creates shell classes at run time and only the
  *runtime* hive would settle it.
- **Task Manager shows unreadable characters in its User Name column,** for every process
  except *System Idle Process*. The cause is not established; the leading suspicion is that a
  PE ships empty SAM and SECURITY hives (deliberately — see the note in `internal/build/hives.go`), so
  there is no account database to resolve a process owner against. Cosmetic: only that column
  is affected, and the rest of Task Manager works.

## Build host

See [`docs/install.md`](install.md) for build prerequisites and per-platform setup (Linux,
Windows, macOS).
