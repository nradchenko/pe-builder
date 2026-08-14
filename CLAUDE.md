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

- `cmd/pebuild/` — the command-line frontend (cobra). Besides building, it exposes the
  engine's readers as inspection commands: `source probe|extract|ls|dirs|inf` reads a
  Windows installation source (identity, a file pulled out and decompressed from
  whichever form the media stores it in, the declared file list with each file's storage
  form and output path, the directory-ID table, and an INF section as the build's own
  parser sees it), and `hive dump` prints a key or value from a registry hive in a
  source, a built PE, or a bare hive file. `run` boots an image `build` already produced —
  named directly, or through the manifest that wrote it — in QEMU, reading the image's own
  architecture to pick the emulator and machine; it deliberately does not build, and is a
  plain launcher. Its `--serial` attaches COM1 to a QEMU chardev, which is what an image built
  with the `serialdebug` plugin needs to be debuggable — that plugin turns the kernel debugger
  on, and a debug image booted with no debugger attached suppresses its own output rather than
  producing any. `verify` answers the question an
  image on disk otherwise cannot — was this built by the pebuild I have now, from this
  manifest? — by reading the receipt the image carries and recomputing every field from
  the current binary, manifest and plugin directories, naming which of engine, manifest,
  source, plugin set or options differs. With no manifest to be found it just prints the
  receipt, so it stays useful away from the tree that built the image. It exits non-zero
  on a mismatch, and keeps three outcomes apart that are easy to conflate: a mismatch, an
  input that could not be checked, and an image carrying no receipt at all.
- `cmd/pewalk/` — the `pewalk` command: an "ldd for PE" that computes and renders the
  import closure of a Windows binary against a source (cobra).
- Engine packages, roughly in pipeline order:
  - `manifest/` — the HCL build manifest (load + validate).
  - `source/` — read a Windows installation source (directory or ISO) and probe its
    identity (build number, architecture, flavor). It also owns the source's *areas*:
    a source keeps its files in two top-level folders — the OS payload and the
    real-mode boot chain — which are the same `I386` folder on x86 media and split on
    amd64 media, where the payload moves to `AMD64` and the boot chain (being real
    mode) stays in `I386`. `Identity.Roots` resolves the pair, and every stage that
    reads from or writes under a source folder resolves it through that rather than
    naming one, so they cannot disagree.
  - `compress/` — decompress the source's MSCF cabinets (`.??_` files, `DRIVER.CAB`,
    the `ASMS*.CAB` assembly cabinets). A cabinet declares its compression per folder,
    so one may mix codecs: LZX covers essentially everything on x86 media, while amd64
    media packs the side-by-side assemblies with MSZIP (deflate per block, each block
    decoded against its predecessor's output as a preset dictionary).
  - `inf/` — a Windows setupapi-compatible `.inf` parser, and the PE adaptation of a network
    component's INF: a stock one applies security descriptors a PE has no API to apply and
    starts co-services a minimal PE does not run, so a staged copy has both removed. It
    rewrites the *user's* file at build time rather than shipping an adapted copy of
    Microsoft's, which is what keeps it clean-room.
  - `layout/` — resolve the source's `txtsetup.sif` / `layout.inf` (file → target
    directory, on-disk name, boot-driver lists, and the source's locale from the
    `[nls]` section). It also settles whether the source is a workstation or a server
    product — the one thing separating XP Professional x64 Edition from Server 2003 x64,
    which are otherwise the same build number and architecture — from the product type
    the media's own `hivesys.inf` declares, falling back for a source that declares none
    to the SKU in `txtsetup.sif`'s `[SetupData] ProductType`.
  - `action/` — the build action IR (copy-file, mkdir, registry, text-edit) that the
    base layer and plugins compile to. A copy or mkdir carries the source area it
    works in (see `source/`); a copy is read from and written to the same area, so it
    is one property of the action rather than two.
  - `inifile/` — apply the text/INI edit actions (e.g. the `txtsetup.sif` boot edits)
    to files in the assembled tree.
  - `plugin/` — plugin discovery, parsing, dependency resolution (auto-enable the
    transitive `depends` closure and order it deps-first), evaluation into actions, the
    `[Build]` pre-pass that compiles a build-capable plugin's payload from source (in a copy
    of the plugin's own directory, into which the engine also materializes the build support
    every plugin may use — currently the shared Windows `VERSIONINFO` template, under
    `pebuild/`, so one copy serves them all rather than each plugin shipping its own), the
    `[Closure]` section that computes a plugin's file set from the static import closure
    of its seed modules (the shared engine the base layer also uses), the `[SourcePaths]`
    section that copies a source file by its exact path (for a basename a name lookup
    can't disambiguate), the `[Options]` section that declares manifest-settable knobs (with
    defaults, an optional type, and an allowed set) which the manifest overrides and the
    engine substitutes one-to-one into the plugin's INF values via `$(name)` placeholders,
    the `.<arch>` section suffix that scopes entries to one architecture (read alongside the
    unsuffixed section, as a `.BuildNo` variant is), and installing plugins from
    `.cab`/`.zip` archives (`plugins add`).
  - `base/` — the clean-room base layer: a hand-curated seed and registry/boot edits in
    `base.inf`, the boot-driver and locale derivation from the source, and the
    `pewalk`-based import-closure scanner that computes the import-derivable half of the
    boot file set from the source's own PE dependency graph (so it is not hand-listed).
    The seed also has to carry what no import scan can see — a module the registry names by
    file name and loads at run time, like `psbase.dll` or the `rsaenh.dll` cryptographic
    provider that the SOFTWARE hive we build registers: those are staged because we create
    the registration that would otherwise dangle. Where a driver has to reach a device that
    only appears at run time, the seed binds it through the Critical Device Database, which
    matches on a device ID rather than on a registry path the build would have to predict —
    that is how the processor driver reaches the CPU that ACPI enumerates at boot, and it is
    what lets the CPU idle rather than spin, since a PE runs no driver-install pass.
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
  - `receipt/` — the record a build leaves inside the image it produced (`pebuild.txt` at
    the tree root, `PEBUILD.TXT` once the ISO stage uppercases the tree, so a booted PE can
    `type` it). It describes the build's *inputs* — the engine content hash, the manifest
    digest, the source fingerprint, and the fully resolved plugin set with the option values
    it settled on — not the image it sits in, which it cannot hash from within. It also
    carries the build's **taint** verdict: a build that used anything outside the vetted
    in-tree set (pebuild's embedded built-ins and its base layer) records graded reasons —
    an out-of-tree plugin, one whose `[Build]` ran, one that staged files it carried, or a
    manifest `inject{}` — and the build logs one `build tainted:` line. Taint records what
    happened; it never refuses a build (`build.allow_plugin_builds` remains the guardrail
    that decides what may run). The engine hash covers what is `go:embed`ed rather than the
    executable, whose bytes change on every link; a plugin pebuild does not embed carries its
    own content hash instead.
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
- `plugins/` — the built-in plugins bundled with pebuild (embedded via `go:embed`). Three kinds:
  **build-capable** (ships source + a `[Build]` declaration compiled by the pre-pass, not a prebuilt
  binary — `poweroff`, `ramdrive`, `pesuite`, `explorer-shell`, `hardware`, `startup`),
  **closure-computed** (a `[Closure]` seed instead of a hand file list — `win32-gui`, `system-tools`,
  `accessories`, `games`, `network-tools`), and plain `.inf`-only (`luna`, `wallpaper`, `serialdebug`,
  `terminal-services`, `storage-pnp`, `network-ui`, `boot-prompt`). **Each plugin's own `README.md` holds its detail**
  — mechanisms, decisions, gotchas; the index below is one line each, grouped by role:
  - *Desktop:* `explorer-shell` boots to the Explorer desktop (ships `peshell`/`peshut`; owns the
    Start-menu and COM-self-registration contracts other plugins contribute to; the Start-menu planner
    `startmenu.c` has a host golden test in `smtest/`), with its supporting chain — `win32-gui` (shared
    Win32 GUI runtime), `storage-pnp` (rebuilds the PnP tree so disks get a drive letter), `ramdrive`
    (from-scratch WDM RAM-disk driver → a writable profile with no attached disk), and `system-tools` /
    `accessories` (optional GUI utilities).
  - *The XP look:* `pesuite` (system-start driver setting the product SuiteMask the session subsystem
    gates on) → `terminal-services` (the session stack) → `luna` (the "Luna" visual style + Themes
    service); `wallpaper` (the XP wallpaper via Active Desktop). Each depends leftward.
  - *Networking* (each pulls `hardware` in): `hardware` ships `pehw`, the boot-time PnP driver-install
    + `INetCfg` network-config pass; `network-tools` the CLI utilities (`ipconfig`, `ping`, …);
    `network-ui` the Network Connections folder, tray icon and adapter dialogs.
  - *Infrastructure:* `startup` — the `HKLM\SOFTWARE\pe-builder\Startup` one-shot-task contract + the
    `perun` runner that walks it (from `peshell` on a desktop build, `startnet.cmd` on a console one);
    `hardware` is its first consumer.
  - *Standalone:* `poweroff` (shutdown command; the reference build-capable plugin, driving the shell's
    Turn Off via `peshut`), `games` (the XP games a minimal PE can run), `serialdebug` (turns the kernel
    debugger on over COM1; `pebuild run --serial` attaches), `boot-prompt` (stages the source's
    `bootfix.bin`, so the CD asks "press any key to boot from CD" — but only on a machine whose
    first hard disk has an active partition, which is the file's own condition, so a diskless VM
    boots straight in either way; the base layer stages only what booting requires).
- `internal/` — support packages: `golden` (golden-file test helper), `buildinfo`
  (version metadata, plus the product/author/copyright a plugin's binaries stamp into their
  Windows version resource — passed to a plugin's build in the environment so the several
  binaries cannot disagree about who wrote them or under what licence, along with the version
  reduced to the four numbers a `FILEVERSION` takes, so no plugin restates that in shell),
  `enginehash` (a
  deterministic content hash over the file trees pebuild embeds — the base layer and the
  built-in plugins — which is what a build receipt records in place of a hash of the
  executable).
- `scripts/` — repository tooling. `gen-licenses.sh` (+ its template) regenerates the
  committed `THIRD-PARTY-LICENSES` notice from the binaries' transitive dependency closure
  via `go-licenses`; run it through `make licenses`.
- `docs/` — project documentation.
  - `docs/supported.md` — the compatibility matrix: which Windows sources are supported and
    what each produces (XP SP3 32-bit and XP x64 both reach the themed desktop, and
    Server 2003 reaches the Explorer desktop on both architectures), including the gaps
    that track the source's *codebase* rather than its architecture,
    the output targets, and the guest hardware an image expects. Keep it current when a
    source, target, or environment changes status — it is where "does X work?" is answered,
    and it distinguishes **verified** from **untested** deliberately.
  - `docs/plugins/` — the plugin bundling/inclusion policy (what may ship built-in).
  - `docs/reference/` — a primary-source reference describing the **original**
    Bart's PE Builder: its build process, the plugin `.inf` format, conventions,
    and internals. Treat these as the authoritative model of how the original tool
    behaves, and consult them before implementing anything format-related.

## Inspecting a source or a build

**Use the tools, not a one-off script.** Questions about a Windows source or a built
PE — what is on the media, in what form, where it would land, what a registry hive or
an INF says — are answered by `pebuild`'s own inspection commands. They read the media
exactly as a build does, so what they report is what the engine sees.

| Question | Command |
|---|---|
| What is this media? | `pebuild source probe <src>` |
| Get me this file, decompressed | `pebuild source extract <src> <file>... [-o dir\|-]` |
| Is this file on the media, and where does it land? | `pebuild source ls <src> [pattern]` |
| What directory does this ID mean? | `pebuild source dirs <src>` |
| What does this INF section say? | `pebuild source inf <src> <file> [section]` |
| What is in this registry hive? | `pebuild hive dump <target> [path]` |
| What was this image built from? | `pebuild verify <image> [manifest]` |
| What files are in this built image? | `pebuild image ls <image> [pattern]` |
| Get me this file out of a built image | `pebuild image extract <image> <file>... [-o dir\|-]` |
| What does this binary import? | `pewalk --iso <src> <binary>` |

`source extract` resolves a bare name against all three forms the media stores files in
(loose, compressed `*.??_`, packed in `DRIVER.CAB`/`SP*.CAB`), takes an exact path when
a basename is ambiguous, and pipes to stdout with `-o -`. `hive dump` reads a source, a
built PE (directory or mastered `.iso`), or a bare hive file. `verify` reads the same two
image forms and answers from the receipt inside them rather than from the media itself.

The `image` commands read a **built** PE — one this tool produced, or a reference image
built by another tool — as a plain filesystem, with no identity probe and no layout
resolution (a build has already resolved, decompressed and placed everything, so the
`source` commands cannot read one). Matching is case-insensitive, which matters because
the tree writes mixed case and the ISO stage uppercases. Full detail in
[`docs/inspecting.md`](docs/inspecting.md).

So: do **not** mount the ISO, shell out to `7z`/`xorriso`/`cabextract`, hand-parse
`txtsetup.sif`, or write a throwaway Go program to read a hive. If an inspection need
is not covered, extend these commands rather than working around them — the next
session gets the capability instead of rewriting the one-liner.

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