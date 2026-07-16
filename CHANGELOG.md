# Changelog

All notable changes to pe-builder are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/).

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

[0.2.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.2.0
[0.1.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.1.0
