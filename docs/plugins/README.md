# Plugins

A **plugin** adds files, registry entries, and tweaks to the PE you build — a tool,
a driver, a shell, a config change. pe-builder uses the classic Bart's PE Builder
plugin format (an `.inf` describing the changes), so existing community plugins work,
and writing your own is a small text file.

This guide gets you started, and the full `.inf` format is in
[`docs/reference/03-plugin-format.md`](../reference/03-plugin-format.md).

**Before you write a plugin, read the [inclusion policy](inclusion-policy.md) and the
[conventions](conventions.md), and make sure yours meets every requirement and rule in them** —
what may ship built-in, then documentation, C linting, version resources, and architecture
scoping. A plugin our policy keeps out of the bundle is still fully supported as a user-supplied,
out-of-tree plugin — bundling is only about what ships *inside* pe-builder, not what you can build
with.

## Using plugins

**A plugin is a directory** containing an `.inf` (plus any files it ships). pebuild
looks for plugins, in order:

1. the project directory set by `plugins_dir` in your manifest (the examples use
   `./plugins`);
2. any directories in the `PEBUILD_PLUGIN_PATH` environment variable;
3. the per-user global directory (`~/.local/share/pe-builder/plugins`, or
   `$XDG_DATA_HOME/pe-builder/plugins`);
4. the plugins built into pebuild.

A more specific location shadows a less specific one, and a build applies only the
plugins your manifest **enables**, plus the plugins those depend on. (The `plugins
list`/`info` commands additionally look in `./plugins`, so a plugin you install with
`--local` shows up there.)

Nothing else turns a plugin on. In particular, `[PEBuilder] Enable = 1` in a plugin's
own `.inf` — a key of the original tool's format, which pebuild accepts and ignores —
does not enable it; only the manifest does.

**Enable a plugin** in the manifest with a `plugin` block:

```hcl
plugins_dir = "./plugins"       # where this project's plugins live

plugin "poweroff" {             # a built-in: adds a shutdown command
  enabled = true
}
```

**Inspect and install** from the command line:

```sh
pebuild plugins list            # every available plugin and where it resolves from
pebuild plugins info poweroff   # a plugin's details and the actions it compiles to
pebuild plugins add tool.zip    # install a .cab or .zip into the global dir
pebuild plugins add tool.zip --local   # …or into the project ./plugins
```

**Adjust a plugin without editing it** — layer overrides in its manifest block. Paths
are output-tree-relative (`i386/system32/…`); registry paths use `HKLM\…`:

```hcl
plugin "shell" {
  enabled = true
  options {
    theme = "classic"                                  # set a value the plugin declares (below)
  }
  files {
    include = ["i386/system32/extra.dll"]              # pull an extra source file in
    exclude = ["i386/system32/unwanted.dll"]           # drop one the plugin copies
    inject  = [{ from = "./local/x.cfg", to = "i386/system32/x.cfg" }]  # add a local file
  }
  registry {
    set    = { "HKLM\\SOFTWARE\\App\\Flag" = "1" }
    delete = ["HKLM\\SOFTWARE\\App\\Old"]
  }
}
```

An `options {}` block sets the values the plugin **declares** in its `[Options]` section
(see "Let the manifest set a value" below). Setting an option the plugin does not declare,
or a value outside what it allows, is an error that names the plugin and the option.

If a plugin `depends` on another (via `[Update]`), enabling it **auto-enables** the
dependency — and its dependencies in turn — placing each one before the plugin that
needs it. You only enable what you want directly; the graph pulls in the rest. Enabling
a dependency explicitly is still fine. The one conflict is enabling a plugin while
setting `enabled = false` on something it requires: pebuild errors rather than silently
overriding your choice. A dependency cycle is also an error, reported as its path.

## Writing a plugin

Create a directory with an `.inf` named after it. A minimal plugin that ships a file
and sets a registry value:

```ini
; mytool/mytool.inf
[Version]
Signature = "$Windows NT$"          ; required

[PEBuilder]
Name   = "My Tool"

[SourceDisksFiles]
; file = directoryID[, renamedTo][, attribute]   (2 = system32)
; attribute 1 = the file ships with the plugin (in this directory)
mytool.exe = 2,,1

[Software.AddReg]
; type,"subkey","value","data"   (0x1 = REG_SZ)
0x1,"MyTool","Installed","yes"
```

**Where a file comes from:** a file the plugin's `[Build]` produces (below) is taken
from the build output; a `[SourceDisksFiles]` entry with attribute `1` is read from the
plugin's own directory (ship it alongside the `.inf`); anything else is taken from the
user's Windows source (name a Microsoft file the source already provides). Registry
edits go to the SOFTWARE (`[Software.AddReg]`), SYSTEM
(`[SetupReg.AddReg]`), or default-user (`[Default.AddReg]`) hive; `[WinntDirectories]`
defines custom target folders.

**Ship source, not a binary.** For a plugin that installs a tool you wrote, add a
`[Build]` section: pebuild compiles the source at build time and places the result,
so nothing opaque is bundled. See `plugins/poweroff/` for the reference:

```ini
[Build]
Requires = i686-w64-mingw32-gcc make   ; tools checked on PATH first
Produces = poweroff.exe                ; the built file, served to [SourceDisksFiles]
; Command defaults to `make`, run in a temp copy of the plugin directory
```

Built-in plugins build automatically. A build-capable plugin from a drop-in directory
runs an arbitrary command, so it is refused unless you opt in with
`build.allow_plugin_builds = true` (or `pebuild build --allow-plugin-builds`).

**Compute a file set instead of listing it.** When a plugin needs a Windows component
and everything that component depends on — a GUI runtime, a subsystem — listing every
DLL by hand is brittle. A `[Closure]` section names a few *seed* modules; pebuild walks
their **static PE import graph** against the source and places the seeds plus every
dependency it finds, so you name the roots and the dependencies come for free:

```ini
[Closure]
; one seed module per line; its static import closure is computed from the source
shell32.dll
comctl32.dll
ole32.dll
```

A module the source does not carry is dropped (anything genuinely needed is on the
media). Only **static** imports are followed — delay-load imports resolve lazily at
runtime and mostly never do, so following them would pull in large subsystems a minimal
environment never uses; a genuinely-needed runtime load goes in `[SourceDisksFiles]` as
the plugin's own seed. `[Closure]` only reads import tables (no code runs), so unlike
`[Build]` it needs no opt-in. It is purely additive: a plugin with no `[Closure]` behaves
exactly as before. See `plugins/win32-gui/` for the reference.

**Know what a closure cannot see.** `[Closure]` follows import tables, so it finds only what
a binary *links* to. Windows loads plenty of things by **name from the registry** instead, and
no scan of any binary points at them:

- a **cryptographic provider** — the registry gives its file name (`Image Path = rsaenh.dll`),
  and CryptoAPI loads that;
- a **COM in-process server** — reached by CLSID from the registry, not by an import;
- a **service DLL**, a hook DLL, a codec — likewise named by a registry value.

These fail *quietly*: the caller gets an error nobody prints, and the feature simply does
nothing. Worse, the file being present proves nothing — a COM server that is staged but never
registered is just as inert, because **staging is not registering**. When a plugin turns on a
Windows feature, ask what that feature loads *by name at run time*, add those to
`[SourceDisksFiles]` yourself, and make sure whatever registers them actually runs.

**Copy a source file by its exact path.** `[SourceDisksFiles]` names files by basename,
which the source resolves through its layout. When a file's basename is not unique — a
nested side-by-side assembly, a duplicate name in another folder — name it by its exact
path with `[SourcePaths]`, one `sourcePath = outputTreePath` per line:

```ini
[SourcePaths]
; path within the source's install folder = literal output-tree path
ASMS\6000\MSFT\WINDOWS\COMMON\CONTROLS\COMCTL32.DLL = i386\WinSxS\<assembly>\comctl32.dll
```

The left path is relative to the source's install folder (`i386`); the right is the
literal destination in the output tree. It addresses **loose or compressed** (`.??_`)
source files by path; a cab-packed file has no filesystem path, so reach those with
`[SourceDisksFiles]` by basename instead.

**Let the manifest set a value.** When a value belongs in the plugin but a build should
be able to change it — a screen resolution, a size, a name — declare it as an *option* in
an `[Options]` section and reference it with a `$(name)` placeholder in the value. Each
line declares one option, `name = default[, type][, allowed...]`:

```ini
[Options]
; name = default[, type][, allowed...]   (type: string | int | bool; string if omitted)
width  = 1024, int
height = 768,  int
bpp    = 32,   int, 8, 16, 24, 32         ; only these values are accepted

[SetupReg.AddReg]
0x4,"...\Video\0000","DefaultSettings.XResolution",$(width)
0x4,"...\Video\0000","DefaultSettings.YResolution",$(height)
0x4,"...\Video\0000","DefaultSettings.BitsPerPel",$(bpp)
```

A manifest overrides an option in the plugin's `options {}` block (above); an override is
validated against the declared type and allowed set, so a bad value is a clear error
rather than a broken build. When the manifest does not set an option, its **default**
applies. Substitution is **one-to-one text**: pebuild replaces each `$(name)` in the
value with the option's value (override or default) before the value is compiled — so an
option lands wherever the value does. Placeholders are honoured in the **value data** of
registry and text edits (`[…AddReg]` data, `[SetValue]`/`[AddLine]`/`[DelLine]` values).
Once a plugin declares options, a `$(name)` that names none of them is an error, catching
a typo rather than shipping it literally. A plugin with no `[Options]` opts out entirely:
its values are never scanned, so a literal `$(` is left untouched.

To share a plugin, zip or cab its directory; users install it with `pebuild plugins
add`. To propose one for bundling with pe-builder, see
[`inclusion-policy.md`](inclusion-policy.md).