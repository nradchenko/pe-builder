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

Both `pebuild` and `pewalk` support shell completion (bash, zsh, fish, PowerShell) — see
[shell completion](docs/install.md#shell-completion) to enable it.

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
source (a directory or `.iso`), it computes the binary's transitive **import closure** and
reports where each module lives, flagging any the source lacks. See
[`docs/pewalk.md`](docs/pewalk.md).

## Documentation

See [`docs/`](docs/README.md) for the full index — installation, supported sources, the
inspection tools, the WIM layout, plugins, and the Bart's PE Builder reference.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for how to get started, the
conventions, and how AI-assisted contributions are handled. To report a security problem, follow
[SECURITY.md](SECURITY.md) rather than filing a public issue.

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
