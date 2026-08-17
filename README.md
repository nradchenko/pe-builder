# pe-builder

An open-source, cross-platform tool for building bootable
[**Windows PE**](https://en.wikipedia.org/wiki/Windows_Preinstallation_Environment)
environments — a Windows **live CD** — from a **Windows XP or Server 2003** installation
source. A modern, Linux-first revival of the ideas behind the classic, discontinued
[**Bart's PE Builder**](https://en.wikipedia.org/wiki/BartPE).

![A Windows XP live desktop built with pe-builder: the Luna visual style, the modern Start
menu, About Windows showing Service Pack 3, and the CD drive's properties — all running from a
~68 MB CD with no attached disk.](docs/images/desktop.png)

## What it can do

- **Build a bootable PE from Windows XP or Server 2003** — 32- or 64-bit sources alike.
- **Boot to a full XP desktop** — the Explorer shell with the **"Luna" visual style**, running
  entirely from the CD with no attached disk.
- **Self-configuring networking** — a PE installs its own devices and picks up a DHCP address at
  boot (32-bit XP sources today).
- **Compact WIM boot** — an optional `wim` layout packs the whole image into a compressed `.wim`
  for a markedly smaller ISO.
- **Extend it with plugins** — a plugin system in the spirit of BartPE's (a similar, though not
  drop-in, format), with built-ins for the shell, networking, games and utilities.
- **Localized to the source** — the console and desktop follow the source's own code pages, fonts
  and keyboard, down to double-byte locales like **Simplified Chinese**.
- **Runs on any host OS** — build on Linux, Windows or macOS.

> **Status: early development.** The images above are early proofs of concept. Tagged releases,
> with prebuilt binaries, are on the
> [releases page](https://github.com/nradchenko/pe-builder/releases); formats, commands, and
> structure may still change between them.

## What this is

The original *Bart's PE Builder* (freeware, Windows-only, now discontinued and
never open-sourced) assembled a bootable live Windows environment — "BartPE" —
from a genuine Windows XP / Server 2003 installation, driven by a simple but
powerful plugin system. Its plugin ecosystem, including the well-known desktop
shell plugin, turned a bare live environment into something close to a full Windows
desktop.

**pe-builder** is a clean-room, open-source reimplementation of that idea, with two
goals:

- **Run anywhere** — build on Linux (and other platforms), not just Windows.
- **Stay compatible** — understand the original plugin format so the existing
  body of community plugins can be reused.

## Bring your own Windows

Like the original, pe-builder ships **no Microsoft files**. You supply your own licensed
Windows installation source, 32- or 64-bit; the tool only reads and repackages files from
the source you provide.

See [`docs/supported.md`](docs/supported.md) for which sources are supported and what each
produces, and for the hardware an image expects.

## Status

A build runs in a few seconds and produces an ISO of roughly 44 MB for a bare `cmd.exe` image
up to ~63 MB for the themed desktop. The x64 path is newer and more experimental than the
well-worn 32-bit one, so expect more rough edges. Images are developed and tested in **QEMU**;
booting on physical hardware is untested.

See [`docs/supported.md`](docs/supported.md) for what each source produces and the hardware it
expects, and [`docs/plugins/`](docs/plugins/) to use or write plugins.

## Quick start

You need a supported Windows installation source (an `.iso` or an extracted directory). A
desktop image also needs the MinGW-w64 cross-compiler.

> **Platforms.** Linux is the primary, tested platform. Windows is tested too — all image
> types build and boot, with some environment setup — and macOS is experimental (it builds,
> but is not yet run). See [`docs/install.md`](docs/install.md) for per-platform setup.

1. **Get `pebuild`.** Download the prebuilt binary for your OS from the
   [releases page](https://github.com/nradchenko/pe-builder/releases), and put it on your
   `PATH`. Or, with Go installed, build it from source:

   ```sh
   go install github.com/nradchenko/pe-builder/cmd/pebuild@latest
   ```

2. **Grab a manifest and point it at your Windows media.** Save
   [`examples/luna.hcl`](examples/luna.hcl) (the themed desktop) and set `source.path` to your
   `.iso` or extracted source directory.

3. **Build the image.**

   ```sh
   pebuild build luna.hcl
   ```

4. **Boot it in QEMU.**

   ```sh
   pebuild run luna.hcl
   ```

   `run` boots the image the manifest produced — it never builds one — and reads the image to
   choose the emulator and machine, so the same command works for a 32- or 64-bit image.

This builds the themed XP **"Luna"** desktop. Point `build`/`run` at a different manifest for
another image:

- [`examples/minimal.hcl`](examples/minimal.hcl) — a bare interactive `cmd.exe` (needs no
  cross-compiler).
- [`examples/explorer-shell.hcl`](examples/explorer-shell.hcl) — the desktop in the classic
  Windows 2000 look.

Each manifest lists the plugins it enables; see [`docs/plugins/`](docs/plugins/) to add more
(wallpaper, games, networking, and the rest). Rebuilds from the same source reuse a
decompression cache under `~/.cache/pe-builder`, so iterating on a manifest stays fast.

### Booting from a WIM (experimental)

By default a build masters the file tree straight onto the ISO as loose files. Setting
`layout = "wim"` in the manifest instead packs the whole tree into a single compressed
Windows Imaging (`.wim`) image and boots the system out of it — the running OS reads its files
on demand from the WIM — so the image is markedly smaller than the loose-file layout. It is the
same idea as Hiren's BootCD "Mini XP", but built from an unmodified loader and an open-source
overlay filter, from your own licensed Windows, with no patched or redistributed Microsoft
binaries. It requires an ISO target.

This path is **experimental** and needs three open-source plugins that live outside this
repository, installed with `pebuild plugins add`: `wimmf` (the WIM overlay filter driver),
`firadisk` (reclaims the RAM region as a disk), and `grub4dos` (the loader). The built-in
`wim-cd-boot` plugin, which `layout = "wim"` enables for you, wires them together; without
them installed the build stops and says which is missing.

See [`docs/wim-boot.md`](docs/wim-boot.md) for how the chain works and a full walkthrough.

## Looking inside a source or a build

`pebuild source` inspects a Windows installation source without unpacking it by hand —
extract a file, list where the media keeps each one and where a build would place it,
read the directory-ID table or an INF section. `pebuild hive dump` prints a registry key
or value out of a source, a PE you just built, or a bare hive file.

`pebuild verify` answers what an image on disk was built from. Every build leaves a
receipt inside the image — `PEBUILD.TXT` at its root, readable from the booted
environment itself — recording the manifest, the Windows source, and the plugins and
option values the build settled on; `verify` reads it back and reports what has changed
since, so a stale ISO is not indistinguishable from a fresh one. See
[`docs/inspecting.md`](docs/inspecting.md).

## Inspecting dependencies with `pewalk`

`pewalk` is a standalone companion tool — an "ldd for PE". Given a Windows binary and a
source (a directory or `.iso`), it computes the binary's transitive **import
closure** — every module it depends on, directly or indirectly, through both static and
delay-load imports — and reports where each one lives in the source. It resolves modules
in every form a Windows source stores them: loose files, compressed `.??_` files, and
files packed inside cabinets such as `DRIVER.CAB`. A module that the source does not
provide is flagged as **missing** rather than treated as an error, which makes it easy to
see what a binary would need but the media lacks.

```sh
go build -o pewalk ./cmd/pewalk

# the import closure of Explorer, resolved inside an ISO
./pewalk --iso win.iso explorer.exe

# just the modules that are missing from the source
./pewalk --iso win.iso --missing-only ntoskrnl.exe win32k.sys

# only the static imports — the modules that must be present for the seed to load
./pewalk --iso win.iso --static-only ntoskrnl.exe
```

Delay-load imports resolve lazily at runtime and often never load in a minimal
environment, so `--static-only` narrows the closure to the static import graph — the
boot-necessary set — dropping the delay-only branches the default walk follows.

Output is a dependency tree by default, or `--format dot` (Graphviz) / `--format json`.
The closure is reported at DLL granularity — which modules a binary imports, not which
functions.

A full closure of a GUI binary is large and densely connected (a shell like
`explorer.exe` reaches ~125 modules through `shell32`, and `kernel32` alone is imported by
almost all of them), so a naive `--format dot | dot` produces an enormous, unreadable
canvas. To get a legible graph, either bound the depth or run the DOT through Graphviz's
transitive-reduction filter `tred` first:

```sh
# bound the recursion for a compact picture
./pewalk --iso win.iso --max-depth 2 explorer.exe --format dot | dot -Tsvg -o deps.svg

# or reduce the full graph to its essential edges before laying it out
./pewalk --iso win.iso explorer.exe --format dot | tred | dot -Tsvg -o deps.svg
```

The text and JSON formats scale to the full closure without this concern.

## Documentation

- [`docs/install.md`](docs/install.md) — building on each platform (Linux, Windows, macOS)
  and the toolchain a desktop image needs.
- [`docs/supported.md`](docs/supported.md) — which Windows sources are supported and what
  each one produces, the output targets, and the hardware an image expects.
- [`docs/inspecting.md`](docs/inspecting.md) — inspecting a source or a build:
  extracting files, listing what the media carries, reading INF sections and
  registry hives.
- [`docs/wim-boot.md`](docs/wim-boot.md) — the `wim` layout: booting a compact,
  open-source Windows PE from a WIM, how the chain works, and a build walkthrough.
- [`docs/plugins/`](docs/plugins/) — using and writing plugins: a short guide, and
  the policy for what may be bundled with pe-builder.
- [`docs/reference/`](docs/reference/) — a detailed, primary-source reference for
  the original Bart's PE Builder: its build process, the complete plugin `.inf`
  format, conventions, and internals. This is the foundation the implementation is
  built on.

## License

Copyright © 2026 Nikita Radchenko

Licensed under the [Apache License 2.0](LICENSE).

The compiled binaries statically link third-party Go modules under permissive and
MPL-2.0 licenses; their notices are reproduced in
[THIRD-PARTY-LICENSES](THIRD-PARTY-LICENSES), which ships with released binaries.

## Disclaimer

pe-builder is **not affiliated with, endorsed by, or derived from the code of** Bart
Lagerweij, the original PE Builder, or Microsoft. Windows and Windows XP are trademarks
of Microsoft Corporation.

Windows XP is end-of-life and unpatched, so **treat any image you build as untrusted**:
run it in an isolated environment, ideally an offline VM, and never as a working system.
You supply your own licensed Windows source — this project ships no Microsoft files. It
is an experiment for enthusiasts, not a supported product.

See [DISCLAIMER.md](DISCLAIMER.md) for the full text.
