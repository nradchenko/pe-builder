# The build manifest

A build is described by a single HCL file — the manifest. It names the Windows
installation source to build from, what to produce, and which plugins to enable.
`pebuild build` takes it as its argument, or reads `pebuild.hcl` from the current
directory when given none.

The shortest manifest that builds is two lines:

```hcl
source { path = "./winxp-sp3.iso" }
output { path = "./out/pe.iso" }
```

Everything else has a default. [`examples/`](../examples/) holds working manifests to
start from — `minimal.hcl` (a bare `cmd.exe` image), `luna.hcl` (the themed desktop),
`explorer-shell.hcl` (the desktop in the classic Windows 2000 look), and `wim.hcl` (the
WIM layout).

**For the field list — every block, every field, every default — read
[`manifest.hcl`](manifest.hcl) beside this.** It is the catalogue, written as a manifest
both so it reads beside an editor and so the test suite can parse it: a field renamed in
the engine makes it fail to decode instead of leaving it stale. This document is the
other half — what the catalogue cannot state: which fields are required for which
target, how they interact, and why each behaves the way it does.

To check a manifest without building from it:

```sh
pebuild validate pebuild.hcl
```

It reports every schema and semantic error it finds, then summarizes the source, what
the build would produce, and which plugins are enabled — resolved, so a defaulted
`output.target` shows as the value a build would use.

It checks less than it looks like it does, and the gap is worth knowing. It reads the
manifest alone: the paths in it are never resolved, and neither is anything a plugin
declares. So a `source.path` that does not exist, a `plugins_dir` naming a missing
directory, and an `options` key the plugin does not declare all pass `validate` cleanly
and then fail the build. What it does check is this file's own shape — required fields,
the values `target` and `layout` accept, the layout/target pairing, the volume
label's length, and duplicate plugin names.

## Relative paths

**Every path in a manifest is relative to the directory you run `pebuild` from, not
to the manifest file.** That covers `source.path`, `output.path`, `build.work_dir`,
`plugins_dir`, and each `inject`'s `from`. Running a build from elsewhere with a
manifest full of relative paths resolves them against the new directory, which is
usually not what you want; either run from the manifest's own directory, or write
absolute paths.

## `source` — what to build from

`source.path` is the one field always required, and the block's only field; it takes a
directory or an `.iso`, and which one it is gets detected rather than declared.

What the media are — build number, architecture, and whether they are a workstation or a
server product — is read from the media themselves, so a manifest neither states it nor
overrides it. `pebuild source probe` reports what a build would read, and names the
evidence behind the product type. Which sources are supported, and what each produces, is
[`supported.md`](supported.md).

The `iso` target additionally requires an **`.iso`** source, not a directory: a bootable
image reuses the source media's own boot sector, so there has to be one to read. A
directory source is fine for the `none` target.

## `output` — what to produce

The block is required; what it must contain depends on the target it selects.
`volume_label` has one non-obvious limit worth stating: it is an ISO 9660 volume
identifier, a fixed 32-**byte** field, so the cap is on bytes rather than runes — a
multibyte label within 32 characters can still overrun it.

### `target = "iso"`

Masters a bootable El Torito (no-emulation) CD image at `output.path`. This is the
normal case: the artifact is the file you burn, or hand to `pebuild run`.

The image is published atomically: it is mastered into a scratch file beside
`output.path` and renamed onto it, so a build that fails partway never leaves a
half-written image behind.

### `target = "none"`

Assembles the file tree — resolved, decompressed, placed, with the registry hives
built and the build receipt written — and then stops, producing no media.

It is for looking at what a manifest produces without paying for mastering: the output
is a plain directory you can `ls`, diff against another build, or point `pebuild verify`
at, since the receipt is written for this target too.

Two consequences follow from there being no media stage:

- **It takes no `output.path`.** Nothing writes a file, so there is no file to name.
  The tree's location is set by `build.work_dir` instead (below); with no `work_dir`,
  it lands in a fresh temp directory that is left in place and named in the build's
  output. `keep_work` is irrelevant here — nothing consumes the tree, so it always
  survives.
- **`layout = "wim"` cannot be used with it.** That layout is a way of building an ISO,
  so it requires the `iso` target.

`volume_label` is likewise unused — it labels an ISO 9660 volume that is never mastered.

## `layout` — how the ISO is assembled

```hcl
layout = "classic" # classic | wim
```

Top-level, defaulting to `classic`. It is orthogonal to `output.target`: both layouts
produce an ISO, built two different ways and carrying different boot chains.

| Layout | What it produces |
|---|---|
| `classic` | the assembled file tree mastered directly onto the ISO |
| `wim` | the tree packed into a compressed `.wim`, read on demand at run time — a markedly smaller image |

`wim` requires the `iso` target and three out-of-tree plugins installed with `pebuild
plugins add`. It is experimental; [`wim-boot.md`](wim-boot.md) covers how the chain
works and walks through a build.

## `build` — how the build runs

The block is optional, and each of its three fields carries a consequence worth knowing
before you set it.

**`work_dir`** is created if it does not exist, and defaults to a fresh temp directory.
What it *means* changes with the target: for an `iso` build it is scratch space that
mastering consumes, but for the `none` target it is where the artifact ends up.

**`keep_work`** keeps the tree for inspection after mastering. Note what this means for
the *next* build: the whole directory is mastered as-is, so a file left behind by an
earlier build into the same `work_dir` is shipped too. When it matters exactly what an
image contains, build into a fresh directory.

**`allow_plugin_builds`** opts in to running the `[Build]` command of a plugin that
ships source rather than a binary — which means running that plugin's toolchain on your
machine. pebuild's own built-in plugins build regardless; this gate governs plugins you
supplied, so it is off unless you ask for it.

## `plugins_dir` — where to find plugins

```hcl
plugins_dir = "./plugins"
```

Top-level and optional. A directory of plugins to resolve from beyond the built-in ones;
each plugin is a subdirectory containing an `.inf`. Naming a directory that does not
exist, or cannot be read, is an error — you asked for it explicitly, so it is not
silently skipped.

A build resolves plugins from, in precedence order:

1. `plugins_dir`, when set
2. the directories in `PEBUILD_PLUGIN_PATH`
3. the per-user global directory (`pebuild plugins add` installs here by default)
4. the plugins built into pebuild

A build deliberately does **not** pick up a `./plugins` directory implicitly, the way
the interactive `pebuild plugins list`/`info` commands do. A build resolves only from
its declared inputs, so its output cannot depend on which directory you happened to run
it from, and a plugin's `[Build]` command is never picked up by accident.

## `plugin` — enabling and adjusting a plugin

Each block selects a plugin by name and, optionally, adjusts what it contributes. A
plugin the manifest never mentions is not enabled; enabling one automatically enables
the plugins it depends on.

```hcl
plugin "explorer-shell" {
  enabled = true

  options {
    width  = 1280
    height = 1024
  }
}
```

`enabled` defaults to **`true`**, so a bare `plugin "luna" {}` enables it. Setting
`enabled = false` is meaningfully different from leaving the plugin out: it records an
explicit refusal, and a plugin another one *depends* on cannot be disabled this way —
that conflict is rejected rather than quietly resolved. Two blocks naming the same
plugin is an error.

[`docs/plugins/`](plugins/) covers using and writing plugins; `pebuild plugins list`
shows what is available and `pebuild plugins info <name>` what one accepts.

### `options`

Free-form `name = value` settings a plugin declares in its own `[Options]` section, with
its own defaults and, where it declares one, an allowed set. An option the plugin does
not declare, or a value outside the set it accepts, is a build error rather than a
silently ignored line.

Values are strings; numbers and booleans are accepted and converted, so `width = 1280`
and `width = "1280"` are the same thing.

### `files`

Adjusts the file set an enabled plugin contributes, without editing the plugin.

```hcl
files {
  include = ["i386/system32/charmap.exe"]
  exclude = ["i386/system32/mspaint.exe"]
  inject  = [
    { from = "./local/notes.txt", to = "i386/system32/notes.txt" },
  ]
}
```

- **`include`** takes extra files from the *Windows source*, named by the output-tree
  path they should land at. The file is looked up on the media by that path's base name
  and placed where you named it.
- **`exclude`** suppresses files the plugin itself would place, by their output-tree
  path.
- **`inject`** copies a file from the build host into the tree. `from` is a host path
  (relative to the current directory, per [Relative paths](#relative-paths) above); `to`
  is an output-tree path. A missing `from` is reported by name at the start of the
  build, not deep inside tree assembly.

Output-tree paths are forward-slash and case-preserving — `i386/system32/foo.dll`. The
ISO stage uppercases them later; you write them as the tree does.

### `registry`

Adds or removes registry values, layered onto what the plugin writes.

```hcl
registry {
  set = {
    "HKLM\\SOFTWARE\\pe-builder\\StartMenu\\Character Map" = "%SystemRoot%\\system32\\charmap.exe"
  }
  delete = [
    "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Advanced\\EnableBalloonTips",
  ]
}
```

A path is a backslash path led by a hive prefix, and its **last component is the value
name**, not a key. Recognized prefixes: `HKLM\SYSTEM`, `HKLM\SOFTWARE`, `HKU\.DEFAULT`
(or `HKEY_USERS\.DEFAULT`), and `HKCU` / `HKEY_CURRENT_USER` (the default-user hive).

Values written here are **strings (`REG_SZ`) only**. A DWORD, a binary value, or a
multi-string has to come from a plugin's own `AddReg` — there is no way to spell a type
in the manifest. `delete` removes a value by the same path form.

To see what actually landed, read the built image's hives with `pebuild hive dump`
([`inspecting.md`](inspecting.md)).

## What the build records

Every build writes a receipt into what it produces — `pebuild.txt` at the tree root,
which the ISO stage uppercases to `PEBUILD.TXT`, so it is the lowercase name you look
for in a `none`-target tree and the uppercase one a booted PE reads with `type`. It is
checkable with `pebuild verify`, and records the manifest's digest, so an edited
manifest is distinguishable from the one that built the image.

It also records whether the build was **tainted** — whether it used anything outside
pebuild's own vetted, in-tree set. Two things a manifest can do taint a build: enabling
an out-of-tree plugin, and using `inject`, which places host files straight into the
tree with nothing vouching for them. Taint is a record, not a refusal; nothing is
blocked for it, and the build says so once in its log.
