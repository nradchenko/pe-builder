# pe-builder

An open-source, cross-platform tool for building bootable **Windows PE**
environments from a Windows installation source — a modern, Linux-first revival of
the ideas behind the classic, discontinued **Bart's PE Builder**.

> **Status: early development.** A build already produces a bootable PE — an interactive
> `cmd.exe`, a Windows Explorer desktop, or that desktop with the XP **"Luna" visual style**
> (all early proofs of concept). Formats, commands, and structure are subject to change, and
> there are no releases yet.

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
licensed Windows installation source (Windows XP Service Pack 3). The tool only
reads and repackages files from the source you provide.

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

## Quick start

You need Go and a Windows XP SP3 installation source (an `.iso` or an extracted
directory). A desktop image also needs the MinGW-w64 cross-compiler, which builds the
plugins that ship as source rather than as binaries (`apt install gcc-mingw-w64-i686`).
Point the example manifest at your source and build:

```sh
go build -o pebuild ./cmd/pebuild

# edit source.path in examples/luna.hcl to your Windows media, then:
./pebuild build examples/luna.hcl

# boot the result:
qemu-system-i386 -M pc -m 512 -cdrom out/pe-theme.iso -boot d
```

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

With no plugins at all ([`examples/minimal.hcl`](examples/minimal.hcl)) the ISO boots to an
interactive `cmd.exe`, and needs no cross-compiler.

Rebuilding from the same source is faster after the first run: pebuild caches the files it
decompresses out of the source's large driver cabinets under `~/.cache/pe-builder` and reuses
them on later builds, so iterating on plugins and settings doesn't repeat the expensive
extraction each time. Pass `--no-cache` to skip it, `PEBUILD_CACHE_DIR` to relocate the cache,
or `PEBUILD_NO_CACHE=1` to disable it.

This is early, fast-moving work — details change commit to commit.

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
