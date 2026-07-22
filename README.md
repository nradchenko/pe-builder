# pe-builder

An open-source, cross-platform tool for building bootable **Windows PE**
environments — a Windows **live CD** — from a Windows installation source. A modern,
Linux-first revival of the ideas behind the classic, discontinued **Bart's PE Builder**.

![A Windows XP live desktop built with pe-builder: the Luna visual style, the modern Start
menu, About Windows showing Service Pack 3, and the CD drive's properties — all running from a
~68 MB CD with no attached disk.](docs/images/desktop.png)

> **Status: early development.** A build already produces a bootable PE — an interactive
> `cmd.exe`, a Windows Explorer desktop, or that desktop with the XP **"Luna" visual style**
> (all early proofs of concept).
> Formats, commands, and structure are subject to change, and there are no releases yet.

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

Like the original, pe-builder ships **no Microsoft files**. You supply your own
licensed Windows installation source — Windows XP SP3, or Windows XP Professional x64
Edition. The tool only reads and repackages files from the source you provide.

See [`docs/supported.md`](docs/supported.md) for what each source produces, and for the
hardware an image expects.

## Status

Early development. A build produces a bootable PE that reaches an interactive
`cmd.exe`, with the console locale — code pages, fonts, and keyboard — taken from the
source's own locale. The plugin subsystem is wired up: builds apply the plugins a
manifest enables, and pebuild ships a set of built-ins. As early proofs of concept, the
`explorer-shell` plugin boots to a minimal **Windows Explorer desktop** — Start menu,
taskbar, and Control Panel — self-contained from CD alone, with a writable RAM-backed
profile and no attached disk, and the `luna` plugin themes that desktop with the familiar
XP **"Luna" visual style**. Enabling either auto-enables the supporting plugins it needs.
A `poweroff` command ships as a built-in plugin, compiled from source at build time; enable
it in your manifest like any other. A build takes a few seconds and produces an ISO of
roughly 44 MB for the bare `cmd.exe` image up to ~63 MB for the themed desktop. See
[`docs/plugins/`](docs/plugins/) to use or write plugins.

**64-bit sources.** Both 32-bit and x64 sources are supported, and either reaches the same
set of images — from a `cmd.exe` prompt up to the themed Explorer desktop. The x64 path is
newer and more experimental, so expect more rough edges;
[`docs/supported.md`](docs/supported.md) has the detail.

Images are developed and tested in **QEMU**; booting on physical hardware is untested.

## Quick start

You need Go and a Windows XP SP3 installation source (an `.iso` or an extracted
directory). A desktop image also needs the MinGW-w64 cross-compiler, which builds the
plugins that ship as source rather than as binaries (`apt install gcc-mingw-w64-i686`).
Point the example manifest at your source and build:

> **Platforms.** pe-builder is developed and tested on **Linux**, and the released Linux
> binaries are the supported ones. The code is pure Go and cross-compiles to macOS and
> Windows — those binaries are published too, but as **experimental**: they build, and are
> not otherwise tested, so please open an issue if something does not work.
> On Windows the smoothest path is **WSL2** (a normal Linux install inside Windows), where
> everything above works unchanged, `apt` and all.

```sh
go build -o pebuild ./cmd/pebuild

# edit source.path in examples/luna.hcl to your Windows media, then:
./pebuild build examples/luna.hcl

# boot the result in QEMU:
./pebuild run examples/luna.hcl
```

`run` boots an image that `build` already produced — it never builds one itself. Give it
the manifest, and it boots the image that manifest writes; give it an `.iso`, and it boots
that. It reads the image to pick the emulator and machine for it, so the same command
works for a 32- or 64-bit image; [`docs/supported.md`](docs/supported.md) spells out the
QEMU line it amounts to, if you would rather type it yourself.

That boots to a **Windows Explorer desktop** — Start menu, taskbar, Control Panel — with the
familiar XP **"Luna"** look, self-contained from CD with no attached disk. Enabling `luna` is
all it takes; its supporting plugins are pulled in automatically:

```hcl
plugin "luna" {
  enabled = true
}
```

For the same desktop in the classic (Windows 2000) look, enable `explorer-shell` instead — the
plugin `luna` themes ([`examples/explorer-shell.hcl`](examples/explorer-shell.hcl)):

```hcl
plugin "explorer-shell" {
  enabled = true
}
```

Add `wallpaper` for the XP desktop wallpaper, on either look — it brings the components that
paint one, so it adds roughly 4 MB to the ISO and stays opt-in:

```hcl
plugin "wallpaper" {
  enabled = true
}
```

With no plugins at all ([`examples/minimal.hcl`](examples/minimal.hcl)) the ISO boots to an
interactive `cmd.exe`, and needs no cross-compiler.

Rebuilding from the same source is faster after the first run: pebuild caches the files it
decompresses out of the source's large driver cabinets under `~/.cache/pe-builder` and reuses
them on later builds, so iterating on plugins and settings doesn't repeat the expensive
extraction each time. Pass `--no-cache` to skip it, `PEBUILD_CACHE_DIR` to relocate the cache,
or `PEBUILD_NO_CACHE=1` to disable it.

This is early, fast-moving work — details change commit to commit.

## Looking inside a source or a build

`pebuild source` inspects a Windows installation source without unpacking it by hand —
extract a file, list where the media keeps each one and where a build would place it,
read the directory-ID table or an INF section. `pebuild hive dump` prints a registry key
or value out of a source, a PE you just built, or a bare hive file. See
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

- [`docs/supported.md`](docs/supported.md) — which Windows sources are supported and what
  each one produces, the output targets, and the hardware an image expects.
- [`docs/inspecting.md`](docs/inspecting.md) — inspecting a source or a build:
  extracting files, listing what the media carries, reading INF sections and
  registry hives.
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
