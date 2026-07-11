# CLAUDE.md

Guidance for AI assistants and contributors working in this repository.

## Project

**pe-builder** is an open-source, cross-platform tool for building bootable Windows
PE environments from a user-supplied Windows installation source. It is a
clean-room, Linux-first reimplementation of the ideas behind the discontinued
*Bart's PE Builder*, aiming for compatibility with that tool's plugin format. See
[`README.md`](README.md) for the project overview.

## Repository layout

The build **engine** is a set of importable Go packages; `cmd/pebuild` is a thin CLI
over it. `pewalk` is a second, standalone tool (library + CLI) that inspects a source's
PE dependencies.

- `cmd/pebuild/` — the command-line frontend (cobra).
- `cmd/pewalk/` — the `pewalk` command: an "ldd for PE" that computes and renders the
  import closure of a Windows binary against a source (cobra).
- Engine packages, roughly in pipeline order:
  - `manifest/` — the HCL build manifest (load + validate).
  - `source/` — read a Windows installation source (directory or ISO) and probe its
    identity (build number, architecture, flavor).
  - `compress/` — decompress the source's MSCF cabinets (CAB-LZX `.??_` files,
    `DRIVER.CAB`).
  - `inf/` — a Windows setupapi-compatible `.inf` parser.
  - `layout/` — resolve the source's `txtsetup.sif` / `layout.inf` (file → target
    directory, on-disk name, boot-driver lists, and the source's locale from the
    `[nls]` section).
  - `action/` — the build action IR (copy-file, mkdir, registry, text-edit) that the
    base layer and plugins compile to.
  - `inifile/` — apply the text/INI edit actions (e.g. the `txtsetup.sif` boot edits)
    to files in the assembled tree.
  - `plugin/` — plugin discovery, parsing, dependency resolution (auto-enable the
    transitive `depends` closure and order it deps-first), evaluation into actions, the
    `[Build]` pre-pass that compiles a build-capable plugin's payload from source, the
    `[Closure]` section that computes a plugin's file set from the static import closure
    of its seed modules (the shared engine the base layer also uses), and installing
    plugins from `.cab`/`.zip` archives (`plugins add`).
  - `base/` — the clean-room base layer: a hand-curated seed and registry/boot edits in
    `base.inf`, the boot-driver and locale derivation from the source, and the
    `pewalk`-based import-closure scanner that computes the import-derivable half of the
    boot file set from the source's own PE dependency graph (so it is not hand-listed).
  - `tree/` — assemble the output file tree (resolve → decompress → place).
  - `hive/` — build the Windows registry hives (pure-Go `regf` read/write).
  - `target/` — output targets (e.g. a bootable ISO).
  - `build/` — the orchestrator that wires the pipeline together.
- `pewalk/` — the "ldd for PE" library: computes the transitive PE import closure
  (static + delay imports, or static-only for a boot-necessary set) of a binary against a
  source, resolving each imported module
  through the `source` and `compress` backings (loose, compressed-loose `.??_`, and
  cabinet-packed forms) and reporting missing modules as flagged red herrings. Renders
  the closure as text, Graphviz DOT, or JSON. Backs both `cmd/pewalk` and the durable
  import-walk scanners.
- `plugins/` — the built-in plugins bundled with pebuild (embedded via `go:embed`),
  each a directory with an `.inf`; a build-capable one (e.g. `poweroff`) ships source
  and a `[Build]` declaration, not a prebuilt binary; a closure-computed one (e.g.
  `win32-gui`, the shared GUI runtime) declares a `[Closure]` seed instead of a hand
  file list.
- `internal/` — support packages: `golden` (golden-file test helper), `buildinfo`
  (version metadata).
- `docs/` — project documentation.
  - `docs/plugins/` — the plugin bundling/inclusion policy (what may ship built-in).
  - `docs/reference/` — a primary-source reference describing the **original**
    Bart's PE Builder: its build process, the plugin `.inf` format, conventions,
    and internals. Treat these as the authoritative model of how the original tool
    behaves, and consult them before implementing anything format-related.

## Ground rules

- **Clean-room.** This is an independent reimplementation. The original tool's
  source was never released; do not incorporate its binaries or any code from it.
- **No Microsoft files.** The tool operates on a Windows installation source that
  the *user* supplies. Never commit Windows/Microsoft files, nor any third-party
  installation media, to this repository.
- **Reference material stays local.** Any third-party binaries used to study the
  original tool are kept in a local, git-ignored cache and are never committed.

## Conventions

- Primary language: **Go**.
- Build definitions use **HCL**.
- Keep documentation and code descriptive of *what* things do; match the style and
  structure of the surrounding files.