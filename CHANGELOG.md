# Changelog

All notable changes to pe-builder are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/).

## [0.5.0] - 2026-07-26

The 64-bit release. An amd64 Windows source now builds the **full themed desktop** — Explorer
with the Luna visual style, working Control Panel applets, a RAM drive, and a Start menu that
powers the machine off — the same image an x86 build gets. Bart's PE Builder and the tradition
that grew around it built 32-bit images only; a live Windows environment of this kind has not
existed in 64-bit.

Reaching it took three things the build could not do before — compiling a plugin's payload for
the target architecture, decoding the MSZIP cabinets amd64 media pack their side-by-side
assemblies with, and addressing a cabinet member by its exact internal path — and four fixes
without which no shell binary would start at all. The x86-only limitation listed in 0.4.0 is
lifted. `pebuild run`, which boots a built image in QEMU from one command, lands alongside.

### Added

- **The desktop plugins build for amd64.** `explorer-shell`, `poweroff`, `ramdrive` and
  `pesuite` now compile for the target architecture rather than a fixed 32-bit toolchain, so
  an amd64 build gets the same desktop an x86 build does. A build-capable plugin scopes its
  build with `[Build.<arch>]`, each naming that architecture's compiler in `Requires` and
  passing the architecture to its `Command`; the shared `[Build]` keeps what does not vary.
- **MSZIP cabinet decoding.** amd64 media pack the side-by-side assemblies with MSZIP, where
  x86 SP3 media use LZX throughout, so those cabinets could not be read at all.
- **Cabinet members are addressable by their full internal path.** A source may pack a whole
  tree into one cabinet — amd64 media keep the side-by-side assemblies in `ASMS01.CAB` — and
  several assemblies each contain a `comctl32.dll`, distinguished only by their directory. A
  `[SourcePaths]` entry naming an exact path now reaches into a cabinet when the path is not
  a loose file.
- **A `wallpaper` plugin** puts the XP default wallpaper on the desktop, which otherwise comes
  up on a flat colour. It ships no code and needs no toolchain, and enabling it pulls in the
  desktop it decorates. The wallpaper is a JPEG and the classic desktop path paints bitmaps
  only, so the plugin stages the Active Desktop surface that can paint one — which is why it is
  a separate plugin, off by default: it adds about 4 MB to the image, and a build that does not
  enable it pays nothing. The image is scaled to whatever resolution the manifest selects.
- **Plugins can register COM servers of their own.** A plugin that stages one declares it under
  `HKLM\SOFTWARE\pe-builder\SelfRegister`, one value per DLL, and the shell registers each after
  its own validated start-up sequence — which stays fixed and unreorderable. Entries register in
  sorted name order: they are self-contained servers whose order should not matter, so pinning it
  keeps any latent dependency between them failing reproducibly rather than intermittently.
- **`pebuild run`** boots a built image in QEMU. The argument is either an `.iso` or the
  manifest that wrote one — told apart by content, so a name that disagrees with what a file
  actually is does not decide it — and with no argument, `pebuild.hcl` in the current
  directory. It reads the image's own architecture to pick the emulator and machine, so one
  command covers both: a 64-bit image gets `qemu-system-x86_64` (and, where the host cannot
  accelerate it, a CPU model that advertises long mode), which a 32-bit emulator could never
  boot. Hardware acceleration is used when `/dev/kvm` is available and quietly falls back to
  emulation when it is not. `--ram` sizes guest memory and `--usb-tablet` adds a tablet for
  seamless mouse. It deliberately does not build: a manifest naming no built image says so,
  and points at building it.
- **Prebuilt binaries.** This is the first release to ship prebuilt `pebuild` and `pewalk`
  binaries. Linux and Windows are tested; macOS cross-compiles but is not runtime-verified, so
  it is experimental. Building from source is documented per platform in
  [`docs/install.md`](docs/install.md).

### Fixed

- **Shell binaries start on amd64.** Every shell binary an amd64 source ships requires the v6
  Common-Controls side-by-side assembly, and the loader fails a process outright when it
  cannot be resolved — so `explorer`, `notepad`, `taskmgr` and the rest did not start at all.
  It is not the cosmetic difference it is on x86, where the assembly only decides whether
  property sheets draw themed controls. The assembly, its manifest, the publisher policy and
  their catalogs are now staged from the cabinet that holds them.
- **The publisher policy is staged where a PE looks for it.** A PE runs with
  `Setup\SetupType=1`, and in setup mode the loader reads publisher policies from
  `WinSxS\setuppolicies` rather than `WinSxS\Policies`. With only the latter staged the
  policy is never consulted: the assembly resolves when asked for by its own version, and
  every request needing the version redirect fails with `14001`, however correct the files
  are. This affects amd64 only — x86 media carry an assembly of the version its binaries ask
  for, so the bind is direct and no policy is involved.
- **Control Panel applets open on amd64.** `rundll32.exe`, which hosts every applet, carries
  no manifest of its own on either architecture, and an assembly reference names an
  architecture — so the x86 manifest shipped for it resolved to nothing on amd64 and every
  applet failed to start. It now gets an architecture-matched one.
- **"Turn Off Computer" powers the machine off on amd64.** The `ExitWindowsEx` hook was built
  and staged for x86 only. Two things stopped it working: a 5-byte relative jump cannot span
  a 64-bit address space, so the patch silently corrupted the function in every process that
  loaded it; and the hook is injected before the host process's C runtime is initialized,
  which aborted the process outright once the toolchain linked one in. The x86 binary is
  unchanged.

### Changed

- **A plugin that cannot be built for the target architecture is refused by the build**, not
  by the parser, and the error names the plugin and the missing section. It stays visible to
  `pebuild plugins list` and `plugins info`, since a plugin that is never built is still a
  plugin worth describing.
- **A plugin build's output is checked against the target architecture.** `make` decides by
  timestamp and no Makefile makes its outputs depend on the architecture, so building a
  plugin for one architecture and then another in the same directory leaves the first one's
  binary in place — which would then simply not run, with nothing in the build output
  explaining why. Each produced file's machine type is now verified.
- **The build refuses to write outside its output tree.** Destinations are assembled from
  plugin-supplied pieces, and nothing rejected a path climbing out of the tree with `..`.

### Known limitations

Full detail for each is in [`docs/supported.md`](docs/supported.md).

- **Folder Options and Taskbar properties do nothing on amd64** — both work on x86.
- **A drive's Properties dialog does not work on amd64** — it works on x86.
- **The `wallpaper` plugin does not render on amd64** — the desktop shows a flat colour; it
  works on x86.
- **Task Manager's User Name column shows unreadable characters** for all but *System Idle
  Process* (cosmetic).
- **There is no sound**, on either architecture — unchanged from 0.4.0.
- **32-bit Windows Server 2003 sources remain untested.** The proven sources are Windows XP
  SP3 (32-bit) and Windows XP Professional x64 Edition.

## [0.4.0] - 2026-07-20

The 64-bit and desktop-maturity release. A **Windows XP Professional x64 Edition** source now
builds and boots — the first time pe-builder produces a PE from anything other than 32-bit
media, and a change that runs through the whole pipeline, since such a source splits its files
across two folders where x86 media uses one. Alongside it the Explorer desktop grows up: the
familiar **modern two-column XP Start menu**, plugins contributing grouped submenus whose
labels come from Windows' own translations, and a general **plugin-options mechanism** so a
manifest can configure what a plugin builds — the screen resolution, the menu style, the
visual style's colours — with a `games` plugin to round it out. A PE also stops burning a CPU
core while it sits idle.

### Added

- **Windows XP Professional x64 Edition sources build and boot.** An amd64 source produces
  a bootable PE that starts to a `cmd.exe` prompt, verified on real media. Such a source
  keeps its files in two folders rather than one — the OS payload in `AMD64`, the real-mode
  boot chain in `I386` — and the build now mirrors that layout instead of assuming a single
  `I386`. The desktop plugins remain x86-only (see *Known limitations*).
- **Architecture-conditional INF sections.** A section may carry an `.<arch>` suffix —
  `[SourceDisksFiles.amd64]`, `[SetValue.i386]` — and is read only when it matches the
  source's architecture, alongside the unsuffixed section, the way a `.BuildNo` variant
  already works. Shared entries stay unsuffixed; only genuinely architecture-specific ones
  move. The base layer uses it for the loader's HAL and for files the 64-bit product does
  not have. An architecture that matches no known one is now rejected rather than silently
  compiling neither variant.
- **`pebuild plugins info --arch`** selects which architecture's sections are reported,
  alongside the existing `--build`.
- **Manifest-settable plugin options.** A plugin declares its knobs in an `[Options]`
  section — a name, a default, and optionally a type and an allowed set — and templates them
  into its own INF values with `$(name)` placeholders; a manifest overrides them in the
  plugin block's `options {}`. Substitution is one-to-one text into value data only, never a
  key or a path, and an unknown option or an out-of-range value is a build error naming the
  plugin. A plugin that declares no options is unaffected.
- **The modern two-column XP Start menu.** The desktop now boots to the familiar XP Start
  panel — pinned items, the right-hand column, All Programs — instead of the classic
  Windows 2000 menu, with the classic menu available as an option. Plugin-contributed
  entries are pinned into the panel in the order the contract lists them.
- **Start-menu submenu paths and localized labels.** An entry's name is now a menu path, so
  `Games\Solitaire` places it in a submenu, and a single leading backslash anchors it at the
  menu root. Its label comes from the target's own `FileDescription` — which Windows ships
  translated — so the menu reads in the source's language with no table of names in the
  build, and an explicit `target|Label` overrides it.
- **Manifest-configurable desktop and theme.** `explorer-shell` exposes the display mode
  (`width`, `height`, `bpp`), the Start-menu style, the version watermark, and whether
  recent documents are shown; `luna` exposes the visual style's colour scheme and size — all
  three schemes and all three sizes already ship inside the one style file, so selecting
  another stages nothing extra.
- **Source and hive inspection commands.** `pebuild source extract`, `ls`, `dirs` and `inf`,
  and `pebuild hive dump`, answer questions about a Windows source or a built PE using the
  same readers a build runs on, rather than an ad-hoc script: a bare file name resolves
  across the loose, compressed `.??_` and cabinet-packed forms alike, an INF section is shown
  as the build's own parser sees it, and a hive is read from a source, a built PE, or a bare
  file — on either architecture, since a hive is looked for across the folders that can hold
  the system area rather than under a fixed `i386`. See
  [`docs/inspecting.md`](docs/inspecting.md).
- **A compatibility reference,** [`docs/supported.md`](docs/supported.md): which Windows
  sources build and what each one produces, the output targets, and the environment an image
  expects — marking throughout what has been verified and what has not.
- **A `games` plugin.** Enabling `games` stages the Windows XP games a minimal PE can
  actually run — Solitaire, Minesweeper, FreeCell and Spider Solitaire — and groups them
  under a *Games* folder in *All Programs*. Each game's file set is computed from its own
  import closure against the source, and its menu label comes from its `FileDescription`,
  so the menu reads in the source's language. Hearts and Pinball are not included: they
  need a network stack and DirectX/sound, which a minimal PE does not have.

### Changed

- **The desktop starts with the modern Start menu, not the classic one.** Builds that want
  the previous look set `classic_start_menu = 1` on `explorer-shell`.
- **Recent documents are hidden, and no longer tracked.** A PE has no meaningful document
  history; `hide_recent_docs = 0` restores both.
- **The Startup folder moved out of `Start Menu\Programs`.** It was showing up as an empty
  *Startup* submenu in All Programs; it is now a sibling of the Start Menu in the profile,
  and still runs what is placed in it.
- **`examples/rescue.hcl` is now `examples/reference.hcl`**, rewritten. It was never a rescue
  image — it is the annotated tour of every manifest block and field — and it described
  plugin options as "carried but not yet acted on", which this release makes false. It now
  documents the full schema against real plugins.
- **`pebuild plugins list` no longer takes `--build`.** Listing enumerates the search path
  without parsing manifests, so the flag selected nothing; `plugins info`, which does parse,
  keeps it.
- **The engine resolves a source's folders from its architecture** instead of assuming
  `i386`. A source keeps its files in two areas — the OS payload and the real-mode boot
  chain — which are the same folder on x86 media and different folders on amd64 media,
  where the payload moves to `AMD64` and the boot chain stays in `I386`. The pipeline now
  reads and writes through a resolved folder pair rather than a fixed name. No change to
  what an x86 build produces: its output tree is byte-for-byte identical.
- **The real-mode boot chain is placed apart from the OS payload.** `setupldr.bin`,
  `ntdetect.com` and `bootfix.bin` run before any 64-bit code does and ship in a source's
  `I386` folder whatever its architecture, so they are now read from and written to that
  folder rather than following the payload. `txtsetup.sif` is metadata, not code, and
  stays with the payload. On an x86 source both land in the same folder as before.
- **Plugin format:** `[SourceDisksFiles]` attribute **16** marks a file as part of the
  real-mode boot chain. It extends the original PE Builder's attribute set, which defines
  nothing above 8 — a plugin written for the original tool never sets it and is read
  unchanged.
- **API (engine packages):** `source.SystemRoot` is **removed** — use `Identity.Roots`.
  `layout.ReadSourceFile`, `tree.Assemble`, `tree.OutputPath`, `hive.BaseHives`,
  `hive.Hives.GraftClasses`, and `boot.WriteStartnet` each take the source's `Roots` (and,
  where the choice varies by file, a `source.Area`). Only importers of the engine packages
  are affected; the `pebuild` CLI is unchanged.
- **A source must now be Windows XP SP3.** The accepted minimum moved up from SP1 to SP3
  (`5.1.2600.5512`), so earlier service packs are refused when the source is probed rather
  than failing somewhere deeper in the build. SP3 was the last and universal XP service
  pack, and it supersedes the earlier ones — building a PE from SP0/SP1/SP2 media has
  almost no practical use, so they are not a target at all rather than merely untested.

### Fixed

- **The CPU idles instead of running flat out.** A booted PE sitting at the desktop doing
  nothing used to run a core at 100% — wasting power and generating heat on real hardware,
  and pinning a host thread in a virtual machine. ACPI enumerates the processor at boot, but
  a PE runs no driver-install pass, so nothing was ever attached to it; with no processor
  driver the kernel's idle loop spins rather than halting. The base layer now stages the
  processor driver and binds it to the CPU by its vendor-neutral device ID, which works
  regardless of which processor the image boots on.
- **"Log Off" is gone from the desktop's shutdown surfaces** — listed as a rough edge in
  0.3.0. There is only one account in a PE, so the entry did nothing. It needed more than a
  policy: the shutdown dialog only honours the setting once `msgina` can load, and `msgina`
  was failing because `odbc32` loads `odbcint` by name — invisible to the import scan — so
  the shell fell back to its own dialog, which ignores the policy. The base layer now stages
  `odbcint.dll`.
- **The Start menu no longer opens itself on first boot.** The shell opened the panel once
  on a fresh profile to draw attention to it; the build now starts past that prompt.
- **A modal "Installation failed." dialog no longer blocks startup on English sources.**
  Some sources register a "Windows Marketplace Link" component whose startup task runs an
  installer over an `.inf` a PE does not ship. The component is removed, which is a no-op on
  sources without it.

### Known limitations

- **There is no sound.** No audio device is set up, and the Windows Audio service is
  deliberately disabled — with it enabled, a system sound blocks the shell's thread and
  cascades every folder window into *Not Responding*. Anything that plays audio runs
  silently instead: the games are playable but mute, and this is why Pinball is not among
  them.
- **The plugins are x86-only, so an amd64 build is a bare `cmd.exe` PE.** Two things stand
  in the way, and neither is a build-system problem: a plugin's INF names its destinations
  under a literal `i386`, which is the wrong folder on an amd64 source; and the plugins that
  ship a kernel driver (`ramdrive`, `pesuite`, and so the Explorer desktop and the Luna
  visual style that depend on them) are compiled 32-bit, which an amd64 kernel will not
  load, and would additionally have to satisfy its driver-signing requirement.
- **32-bit Windows Server 2003 sources are untested.** They are accepted — a 2003 source is
  newer than the SP3 minimum — and may well build, but nothing has been verified against
  one. The sources proven to work are Windows XP SP3 (32-bit) and Windows XP x64 Edition.

## [0.3.0] - 2026-07-17

The themed-desktop release. The `luna` plugin brings the XP "Luna" visual style — the
familiar blue desktop — to a Windows PE, which the classic-shell tradition never managed.

### Added

- **The XP "Luna" visual style** — the `luna` plugin themes the Explorer desktop with the
  familiar blue XP look instead of the classic Windows 2000 one — the theme engine running
  in a Windows PE. Enabling it is a four-line manifest; it pulls in what it needs:
  `terminal-services` and `pesuite`, which give the PE the session subsystem the theme
  server attaches to (a PE boots without one), and the shell's visual-style apply. Three
  colour schemes and three sizes work (blue, normal size by default). A visual-style plugin
  ships no code — it stages a style and the Themes service and selects the style in the
  registry.

### Fixed

- **A missing cryptographic provider** — the SOFTWARE hive registers the RSA provider by
  file name (`rsaenh.dll`), but nothing staged the file, so it was advertised-but-absent and
  every `CryptAcquireContext` call failed silently. The base layer now stages it.
- **Themed controls in Control Panel applets and property sheets** — `explorer.exe` and the
  applet host `rundll32.exe` were not binding to the v6 Common-Controls assembly, so their
  tabs and buttons drew in the classic v5 style around themed pages. `win32-gui` now ships
  an external manifest for each host so they activate v6.

### Known limitations

- Targets Windows XP sources; other source flavors (e.g. Server 2003, x64) and output
  targets are planned.
- The Explorer desktop is an early proof of concept, with rough edges — for example, "Log
  Off" still appears in the shutdown dialog (it does nothing), and dragging files to the
  Recycle Bin is a no-op (use Delete instead).
- With `luna`, themed common controls are enabled for the shell hosts (Explorer and the
  Control Panel applets) via external manifests; the general theme-activation path a real XP
  uses does not take effect in the PE, so some other applications may still draw their
  controls in the classic style.

## [0.2.0] - 2026-07-17

The graphical-desktop release. A build can now boot the PE to a **classic Windows Explorer
desktop** — Start menu, taskbar, Control Panel — self-contained from CD with a writable
RAM-backed profile and no attached disk, assembled from a catalog of bundled plugins. This
release also adds the plugin subsystem, a standalone dependency-analysis tool, and faster
rebuilds.

### Added

- **Plugin support** — build a Windows PE with BartPE-compatible plugins: discovery across
  an ordered search path, load ordering, enable/disable and per-plugin override layers
  (files and registry) from the manifest, and evaluation into the build after the base
  layer. A plugin can ship its own files alongside those taken from the Windows source.
- **Automatic dependencies** — enabling a plugin auto-enables the plugins it depends on,
  transitively, so you enable only what you want directly and the graph pulls in the rest.
- **Build-capable plugins** — a plugin may declare a `[Build]` section that compiles its
  bundled source with a declared toolchain before its files are placed; built-in plugins
  build automatically, while a user-supplied plugin's build is opt-in.
- **Install plugins from archives** — `pebuild plugins add` installs a plugin from a
  `.cab` or `.zip` into the plugins directory.
- **Boot to a Windows Explorer desktop** — an `explorer-shell` plugin boots the PE to a
  classic Explorer desktop (Start menu, taskbar, Control Panel) instead of a bare command
  prompt, self-contained from CD with a writable RAM-backed user profile and no attached
  disk. Enabling it pulls in its supporting plugins automatically: `win32-gui` (the shared
  Win32 GUI runtime), `storage-pnp` (rebuilds the PnP root device tree so disks and volumes
  enumerate and receive drive letters), `ramdrive` (the writable RAM volume), and
  `system-tools` + `accessories` (bundled GUI apps — Registry Editor, Task Manager, Paint,
  Clipboard Viewer).
- **Bundled `ramdrive` plugin** — a writable RAM-drive volume from a from-scratch WDM PnP
  kernel driver (cross-compiled with MinGW-w64, no WDK), lettered via the Mount Manager, so
  the PE has writable storage with no attached disk.
- **Bundled `poweroff` plugin** — a shutdown / power-off command, shipped as source and
  compiled at build time (the reference build-capable plugin); the desktop powers off from
  the Start menu.
- **`pewalk` — an "ldd for PE" tool** — a standalone companion binary that computes a
  Windows binary's transitive import closure against a source (directory or ISO), resolving
  loose, compressed `.??_`, and cabinet-packed modules, and rendering it as a tree, Graphviz
  DOT, or JSON. Used internally to compute a plugin's file set, and useful on its own.
- **Faster rebuilds** — `pebuild` caches the files it decompresses out of the source's
  large driver cabinets under `~/.cache/pe-builder` and reuses them on later builds, so
  iterating on plugins and settings doesn't repeat the expensive extraction. Control it with
  `--no-cache`, `PEBUILD_CACHE_DIR`, or `PEBUILD_NO_CACHE`.
- **Plugin documentation** — a getting-started guide for using and writing plugins, and
  the policy for what may be bundled (`docs/plugins/`).

### Known limitations

- Targets Windows XP sources; other source flavors (e.g. Server 2003, x64) and output
  targets are planned.
- The Explorer desktop is classic-look (no themes) and an early proof of concept.

## [0.1.0] - 2026-07-09

First working release. Builds a bootable Windows PE that boots to an interactive shell,
from a user-supplied Windows XP source, entirely on Linux — no Windows host required. A
build runs in about 4 seconds and produces a roughly 45 MB ISO.

### Added

- **Source reading** — read a Windows installation source (directory or ISO) and probe
  its identity (build number, architecture, flavor).
- **Cabinet decompression** — a pure-Go MSCF (CAB-LZX) decoder for the source's
  compressed files.
- **INF engine** — a Windows setupapi-compatible `.inf` parser that compiles to a build
  action IR (copy-file, make-directory, registry, text-edit).
- **Layout resolution** — resolve the source's `txtsetup.sif` / `layout.inf` (file →
  target directory, on-disc name, boot-driver lists), and derive its locale from the
  `[nls]` section.
- **Clean-room base layer** — boots a source to an interactive `cmd.exe`: a minimal file
  set with the boot, service, and registry configuration, keyboard, mouse, and VGA, plus
  the source's own locale (code pages, console font, keyboard layout).
- **Registry hives** — build the Windows registry hives with a pure-Go `regf`
  reader/writer.
- **File tree and bootable ISO** — assemble the output file tree and master a bootable
  ISO9660 image in pure Go, with no external `mkisofs`/`xorriso` dependency.
- **CLI** (`pebuild`) — `build`, `validate`, `source probe`, and `version`.

### Known limitations

- The image cannot power itself off in the minimal-install environment; power the
  virtual machine off externally.
- Targets Windows XP sources; other source flavors and output targets are planned.
- No plugin system yet — a build is the base layer only.

[0.5.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.5.0
[0.4.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.4.0
[0.3.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.3.0
[0.2.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.2.0
[0.1.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.1.0
