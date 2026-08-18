# Plugin conventions

Standards every bundled plugin meets, and every contributed plugin must meet. They keep the
plugin set consistent, auditable, and identifiable. This is the companion to
[writing a plugin](README.md#writing-a-plugin) (how the format works) and the
[inclusion policy](inclusion-policy.md) (what may ship built-in).

A **trivial** plugin — an `.inf` and nothing else: no source, no `[Build]` — is exempt from the
source-related rules below (linting, versioning). The documentation rule scales down for it too: its
own file header is the whole story.

## Documentation

- **Every non-trivial plugin has a `README.md`** beside its `.inf` (`plugins/<name>/README.md`),
  describing what it does and how — the tasks it performs, the mechanisms it leans on, and the
  decisions, constraints and gotchas behind them.
- **The README holds the detail.** Anything a reader needs in order to understand *why* — a
  mechanism, an alternative that was rejected, a trap, the reasoning behind a value — goes there, in
  named sections, rather than being spread across the plugin's files.
- **A short shared header is fine.** The README, `.inf`, `Makefile` and sources may each open with the
  same brief description of what the plugin is for. Keep them in sync and brief: the moment one starts
  *explaining*, that explanation belongs in the README.
- **Comments in `.inf`/`Makefile`/source are scoped and focused** — they explain the line, section or
  function they sit on, and point at the README for the rest (`see README.md § <Section>`).
- **Detail stays with the code when the code is the context** — a focused comment is right where the
  surrounding lines give more context than the README could: why an argument has that value, an
  ordering constraint, a magic number, a trap the next reader would hit anyway.
- **The README carries the same copyright and licence header** as the plugin's other files (see
  [Copyright headers](#copyright-headers)), written as an HTML comment so it does not render.

The README is repository documentation, not a bundled file — `plugins/embed.go` embeds only what a
build needs, so a README is never added to the embed list.

That embedding has a consequence while you work on a built-in plugin: its `.inf` and sources are
compiled into `pebuild` itself, so a binary built before your edit keeps using the old copy and says
nothing about it. Rebuild after each change, or iterate with `go run ./cmd/pebuild` — see
[Editing a built-in plugin or the base layer](../../CONTRIBUTING.md#editing-a-built-in-plugin-or-the-base-layer).

## Copyright headers

Every file in a plugin opens with a copyright line and an SPDX identifier — the `.inf`, the
`Makefile`, each source file, and the `README.md`:

```
; Copyright 2026 Jane Doe
; SPDX-License-Identifier: Apache-2.0
```

using that file type's comment syntax (`;` in an `.inf`, `#` in a `Makefile`, `//` or `/* */` in C,
and an HTML comment in Markdown so it does not render).

**Put your own name and the year you wrote the file.** pe-builder is Apache-2.0 and asks for no
copyright assignment: contributing does not transfer your copyright, so your files stay yours. The
form is the one in the licence's own appendix, `Copyright [yyyy] [name of copyright owner]`.

- **Editing a file someone else wrote?** Leave its header alone. Apache-2.0 does not require adding
  yourself for a change, and per-file contributor lists grow into noise nobody maintains.
- **Do not bump the year** on later edits, and do not write year ranges. It creates diff churn and
  buys nothing.
- **Do not add yourself to `NOTICE`.** That file carries the attributions Apache-2.0 §4(d) obliges
  *everyone redistributing pe-builder* to reproduce; it is not a contributor list.
- **The SPDX line is always `Apache-2.0`**, whoever holds the copyright — it states the licence the
  contribution is made under, which `CONTRIBUTING.md` fixes for every contribution.

## C source lints clean (cppcheck)

A plugin that ships C source must pass the C linter, **`cppcheck`**, before it lands. The C is
Windows-targeted (Win32 GUI, WDM drivers) and cross-compiled with MinGW-w64, so cppcheck is the tool:
it parses without a compilation database and models the Win32 API via `--library=windows`.

Run it over every C-shipping plugin with:

```sh
make lint-c
```

- **Silence a false positive at the line, with a reason.** cppcheck does not model some Windows
  patterns — a WDM IRP pinned by `STATUS_MORE_PROCESSING_REQUIRED`, a pointer aliasing a caller's
  buffer, a DLL deliberately held for the process lifetime. Suppress those **inline**, with the
  `// cppcheck-suppress <id>` directive on its own line directly above the finding and a one-line why,
  so the annotation documents the pattern.
- **Class-level noise** that is never a real finding here (the `lstrcpyn`/`lstrlen` "use StringCch*"
  advisories, COM-vtable `unusedStructMember`) belongs in the shared `.cppcheck-suppressions` file at
  the repository root, with its reason recorded there.

## Compiled binaries carry a version resource

A plugin that ships a compiled binary — `.exe`, `.dll`, or driver `.sys` — must embed a Windows
`VERSIONINFO` resource, so the shipped PE identifies itself in Windows Properties → Details and in
Task Manager rather than being anonymous.

Fields: `FileDescription`, `FileVersion`, `ProductName`, `ProductVersion`, `CompanyName`,
`LegalCopyright`, `InternalName`, and `OriginalFilename` — the **base name only**, never a
builder-local path.

The engine owns the shared parts, so adding a binary writes no new scaffolding:

- The **template** is materialized into the plugin's build directory as `pebuild/version.rc`; you do
  not ship your own.
- The **values** arrive from `internal/buildinfo` in the build environment as `PEBUILD_VERSION`,
  `PEBUILD_VERSION_NUM`, `PEBUILD_PRODUCT`, `PEBUILD_COMPANY` and `PEBUILD_COPYRIGHT`.

Compile the resource with **`windres`** and add it to the plugin `Makefile`'s link step — copy an
existing plugin's block. The only per-binary parts are the description, internal name, original
filename, and the file type: `VFT_APP` for an exe, `VFT_DLL` for a DLL, and `VFT_DRV` +
`FILESUBTYPE VFT2_DRV_SYSTEM` for a driver `.sys`.

## Architecture-specific files are scoped by architecture

When a plugin stages a file whose name or content varies by architecture, the staging section must be
scoped — `[SourceDisksFiles.<arch>]`, never an unscoped `[SourceDisksFiles]` over an
architecture-specific file. An unscoped entry matches *every* target, so an i386 driver stages into an
amd64 image, the build succeeds, and the only symptom is a driver that does not load at boot.

- **Files pulled from the *source* are the common case.** A name that varies by architecture must be
  scoped wherever it comes from, not only when the plugin ships the file itself. Do not assume the two
  media carry the same set, either — an architecture's file list can be genuinely shorter, not merely
  renamed. Check with `pebuild source ls <src> '<pattern>'` per architecture.
- **Scope it even for a single-architecture plugin.** Single-arch is legitimate; being silent about it
  is not — an unscoped section on an arch-specific file stages the wrong thing on the other target.
- **Scope the registry too.** A target with no matching file variant produces zero copy actions and no
  error; if the plugin's `AddReg` sections are unscoped they still evaluate, leaving a service key
  pointing at a driver that was never staged — worse than a clean omission. Scope those alongside the
  files, or keep the plugin's whole payload behind one architecture gate.

Bundled plugins ship **no prebuilt binaries**: a build-capable plugin compiles its payload from source
with an architecture-matched `[Build.<arch>]` toolchain (checked against the produced PE's machine
type), and everything else is `.inf`-only. A **user-supplied** plugin that does ship a prebuilt binary
scopes its `[SourceDisksFiles.<arch>]` the same way.