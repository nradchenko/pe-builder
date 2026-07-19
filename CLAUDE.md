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
    of its seed modules (the shared engine the base layer also uses), the `[SourcePaths]`
    section that copies a source file by its exact path (for a basename a name lookup
    can't disambiguate), the `[Options]` section that declares manifest-settable knobs (with
    defaults, an optional type, and an allowed set) which the manifest overrides and the
    engine substitutes one-to-one into the plugin's INF values via `$(name)` placeholders,
    and installing plugins from `.cab`/`.zip` archives (`plugins add`).
  - `base/` — the clean-room base layer: a hand-curated seed and registry/boot edits in
    `base.inf`, the boot-driver and locale derivation from the source, and the
    `pewalk`-based import-closure scanner that computes the import-derivable half of the
    boot file set from the source's own PE dependency graph (so it is not hand-listed).
    The seed also has to carry what no import scan can see — a module the registry names by
    file name and loads at run time, like `psbase.dll` or the `rsaenh.dll` cryptographic
    provider that the SOFTWARE hive we build registers: those are staged because we create
    the registration that would otherwise dangle.
  - `tree/` — assemble the output file tree (resolve → decompress → place).
  - `hive/` — build the Windows registry hives (pure-Go `regf` read/write). The SYSTEM hive
    starts from the source's text-setup `setupreg.hiv` and then has the source's `hivesys.inf`
    (the full GUI-mode system config) folded onto it — mapping `CurrentControlSet` to the real
    `ControlSet001`, preserving the text-setup boot config, and keeping win32-service start
    values while leaving boot-driver starts to `txtsetup.sif` — so the Service Control Manager
    has the standard config it needs to start the Win32 services. SOFTWARE and DEFAULT are built
    from `hivesft.inf`/`hivedef.inf`, and the shell build grafts the machine classes from
    `hivecls.inf` into SOFTWARE\Classes.
  - `target/` — output targets (e.g. a bootable ISO).
  - `build/` — the orchestrator that wires the pipeline together.
- `pewalk/` — the "ldd for PE" library: computes the transitive PE import closure
  (static + delay imports, or static-only for a boot-necessary set) of a binary against a
  source, resolving each imported module
  through the `source` and `compress` backings (loose, compressed-loose `.??_`, and
  cabinet-packed forms) and reporting missing modules as flagged red herrings. Renders
  the closure as text, Graphviz DOT, or JSON. Backs both `cmd/pewalk` and the durable
  import-walk scanners.
- `buildcache/` — a small, generic on-disk cache (namespace + caller-supplied key →
  bytes) shared by build stages that recompute deterministic artifacts from an unchanging
  source. Its current use is decompressed driver-cabinet members (keyed by a fingerprint
  of the cabinet's directory region plus the member name), which both the import-closure
  walk and the tree assembler consult so a member decoded on an earlier build of the same
  source is reused instead of re-running a full LZX folder decode. Enabled by default
  (`~/.cache/pe-builder`); `--no-cache`, `PEBUILD_NO_CACHE`, and `PEBUILD_CACHE_DIR` control
  it. A nil cache is a valid disabled cache, so a stage holds one unconditionally.
- `plugins/` — the built-in plugins bundled with pebuild (embedded via `go:embed`),
  each a directory with an `.inf`; a build-capable one (e.g. `poweroff`) ships source
  and a `[Build]` declaration, not a prebuilt binary; a closure-computed one (e.g.
  `win32-gui`, the shared GUI runtime) declares a `[Closure]` seed instead of a hand
  file list. The desktop composes from six: `storage-pnp` rebuilds the PnP root device
  tree so disks/volumes enumerate and get a drive letter; `ramdrive` is a build-capable
  plugin shipping a from-scratch WDM PnP kernel driver (`ramdrive.sys`, cross-compiled with
  MinGW-w64, no WDK) that allocates a RAM-backed disk, self-formats it as a FAT16
  super-floppy, and registers the Mount Manager interface so the volume gets a drive letter —
  a writable scratch home for the profile with no attached disk; `win32-gui` provides the
  shared Win32 GUI runtime; `system-tools` and `accessories` add optional GUI utilities
  (system-administration tools and small accessories), each computing its file set from a
  `[Closure]` seed and contributing Start-menu entries through the shell's registry contract;
  and `explorer-shell` boots to a Windows Explorer desktop instead
  of a bare prompt — it ships `peshell` (a build-capable launcher that self-registers the
  shell/COM components at runtime, sets up the profile, applies the visual style the registry
  selects, and runs explorer) and `peshut` (a hook so the shell's Turn Off powers the machine
  off), and depends on the others. Three more plugins give that desktop the XP look, each a
  layer of one chain: `pesuite` is a build-capable plugin whose tiny system-start driver
  (`pesuite.sys`) sets the product SuiteMask bit the Win32 subsystem gates the whole
  WinStation/session subsystem on — the kernel never writes it in a PE, because a PE needs
  `Setup\SetupType=1` to get its shell run and that makes the kernel skip its product init;
  `terminal-services` (depends on `pesuite`) defines and starts the terminal-services stack,
  whose port the session subsystem waits for, so the PE has a console session at all; and
  `luna` (depends on `explorer-shell` + `terminal-services`) stages the XP "Luna" visual style
  and the Themes service, then selects it in the registry. A visual-style plugin ships no code:
  it only selects a style, and peshell applies it — the theme hook belongs to the client that
  asks for it and dies with that client, so the applier has to be the long-lived shell process.
  Standing apart from that desktop chain, `games` is an optional plugin that stages the XP
  games a minimal PE can actually run (Solitaire, Minesweeper, FreeCell, Spider Solitaire)
  from a `[Closure]` seed each, grouping them under a `Games` submenu via the path-encoded
  Start-menu contract; Hearts and Pinball are left out, needing a network stack and
  DirectX/sound respectively.
- `internal/` — support packages: `golden` (golden-file test helper), `buildinfo`
  (version metadata).
- `scripts/` — repository tooling. `gen-licenses.sh` (+ its template) regenerates the
  committed `THIRD-PARTY-LICENSES` notice from the binaries' transitive dependency closure
  via `go-licenses`; run it through `make licenses`.
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