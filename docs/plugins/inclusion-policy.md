# Bundling and contributing plugins

pe-builder ships a small, curated set of **built-in plugins** and evaluates any
**user-supplied plugins** you place in a plugin directory. This document describes what
may be bundled with pe-builder itself, and how to contribute a plugin for inclusion.

## Two kinds of plugin

- **Built-in** — a handful of plugins we author and ship with pe-builder. The set stays
  small and every entry meets the inclusion criteria below.
- **User-supplied** — any BartPE-format plugin you drop into a local or shared plugin
  directory. pe-builder discovers and evaluates these at build time; we do not
  redistribute them.

## What may be bundled

A plugin is eligible to be bundled with pe-builder only if it meets **all** of:

1. **Ours or redistributable.** It is content we authored, or third-party content under a
   license compatible with pe-builder's (Apache-2.0). We never bundle Microsoft/Windows
   files, non-redistributable freeware, or another project's plugin files.
2. **A manifest, not media.** A file-set plugin bundles its `.inf` — the list of files and
   registry keys, which is our authorship. The files it names are taken from *your*
   Windows source at build time; pe-builder never bundles the Windows files themselves.
3. **Source, not binaries.** A plugin that installs an executable ships its **source** and
   a build declaration, not a prebuilt binary — so every shipped artifact is auditable and
   reproducible.
4. **Self-contained and reproducible.** It builds from its own source with a declared
   toolchain, fetches nothing from the network, and produces the same output every time.
5. **Lightweight.** A small footprint — source and manifests. Large binaries, datasets, or
   media are not bundled.
6. **Broadly useful.** Core or commonly wanted functionality, not a niche one-off.
7. **Maintainable.** We can keep it working as pe-builder evolves.

## The line: manifests and our own source, never third-party binaries

Most third-party plugins cannot be bundled for a simple reason: their substance is a
**binary** we have no right to redistribute — a menu shell, a network configurator, a
RAM-disk driver. pe-builder bundles two things instead:

- **Manifests we author** — an `.inf` that wires up files from *your* Windows source.
  Where a Microsoft file already provides a function, the manifest uses that rather than a
  third-party binary.
- **Our own tools, as source** — small utilities we wrote, compiled from bundled source at
  build time via a `[Build]` step.

Anything whose value lives in a third-party binary is **user-supplied**: you place the
plugin in your plugin directory and pe-builder uses it. A future release may offer
*recipes* that fetch such plugins from their upstream authors — but we never mirror or
redistribute them.

## Reauthoring a plugin (clean-room)

Many useful plugins can be re-created as a bundleable manifest by **reauthoring them
clean-room**: study *what the plugin does* — the files it needs, the registry it sets, the
effect it achieves — and write a new `.inf` from that understanding together with your own
research of the Windows source. Do **not** copy or translate the original plugin's files;
the manifest must be your own work. This is the same discipline pe-builder's base layer is
authored under, and it is what makes, for example, an Explorer-shell plugin possible: our
manifest, wiring Microsoft's own shell components from your source, informed by studying
the effect an existing shell plugin achieves — not derived from its files.

## Contributing a plugin for inclusion

To propose a plugin for bundling:

- Provide a clean-room manifest (`.inf`) — or, for a tool, its source plus a `[Build]`
  declaration.
- Confirm it meets the seven criteria above — in particular: no third-party binaries, no
  Windows files, and reproducible from source.
- For a reauthored plugin, describe the behavior you reproduced and the source you authored
  from, not the original you studied.
- Follow the [plugin conventions](conventions.md) — README, C linting, version resources, and
  architecture scoping.

Plugins that don't qualify for bundling are still fully supported as **user-supplied**
plugins — inclusion is only about what ships *inside* pe-builder, not about what it can
build with.