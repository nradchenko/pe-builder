# 04 — Plugin Conventions, Packaging & Extensions

The [format reference](03-plugin-format.md) covers the `.inf` grammar. This
document covers everything *around* it: how plugins are laid out on disk,
packaged, ordered, configured, and the non-standard extensions that appeared in
the wild.

## Plugin folder layout

Each plugin lives in its own folder under `plugin\`:

```
plugin\
├── bootfix\
│   ├── bootfix.inf      ← the plugin .inf
│   └── bootfix.htm      ← Help= document
├── nu2menu\
│   ├── nu2menu.inf
│   ├── nu2menu.htm
│   └── nu2menu.xml      ← support/data files
└── penetcfg\
    ├── penetcfg.inf
    ├── penetcfg.exe     ← runtime binaries shipped with the plugin
    ├── penetcfg.htm
    ├── penetcfg.ini
    └── ...
```

- A plugin is based on **one or more `.inf` files**. The "main" `.inf` is
  conventionally named after the folder.
- Alongside the `.inf`, a plugin ships whatever it needs: the `Help=` document,
  runtime executables/DLLs to be copied into the build, data files, menu XML,
  configurator executables, etc.
- Files that ship with the plugin and must be present are referenced with
  `[SourceDisksFiles]` attribute `1` (the *pluginCheck* flag).

## Load ordering

PE Builder processes plugins in **alphabetical order** of their folder/`.inf`
name. This ordering is meaningful because later directives layer over earlier
ones (registry values, menu entries, file overrides). Two naming conventions
exploit this:

- **`!`-prefix → loads first.** The bundled **`!custom`** plugin (display name
  "Customize") sorts to the top so user customizations are applied early.
- **`z_` / `zz`-prefix → loads last.** A plugin prefixed to sort at the end runs
  after everything it wants to override. Examples: `z_xpe-custom.inf` (the XPE
  customization plugin) and the bundled **`zz5`** (the `nu2shell` shell). This is
  the idiomatic way to **override or fix another plugin without editing it**.

## The plugin dialog

The plugin dialog lists all discovered plugins; buttons act on the selected
plugin(s):

| Button | Action |
|--------|--------|
| **Enable/Disable** | Toggle the `Enable` flag. This is the only button that operates on a multi-selection. |
| **Config** | Run the plugin's `Config=` configurator (see below). *"The exact function of this button can be different for each plugin — it depends on what the plugin creator uses for configuration."* |
| **Refresh** | Re-read all plugin files (use after adding a plugin folder manually). |
| **Edit** | Open the plugin's main `.inf` in the default text editor. |
| **Add** | Select a `.cab` file; PE Builder extracts it into the `plugin\` folder. |
| **Remove** | Delete a plugin — **removes all its files and its folder** (e.g. removing "BartPE Installer v2" deletes `plugin\peinst\`). |
| **Help** | Open the plugin's `Help=` document/program. |

### `Config=` configurators

The `Config` button runs the command line given by the `[PEBuilder] Config=` key.
This is how a plugin exposes an interactive, plugin-specific configuration UI —
typically a small native helper executable that edits the plugin's own settings.

The bundled example is the **`nu2shell`** plugin (in `zz5`):

```ini
[PEBuilder]
Config=nu2shellcfg.exe @PluginFile@
```

Clicking *Config* launches `nu2shellcfg.exe` with the plugin's file, which (per
the help) lets the user, for example, **enable boot password protection**. The
behaviour is entirely defined by whatever configurator the plugin author ships;
PE Builder just runs the command.

### `Help=` vs `Config=`

Both keys point at external items that PE Builder launches, but they differ:

- **`Help=`** is opened with `ShellExecute()` (by file association) — normally a
  document, though because `ShellExecute` runs any registered type it *can* be an
  executable.
- **`Config=`** is an explicit command line intended to be an interactive
  configurator.

## Packaging & distribution (`.cab`)

Plugins are distributed as Microsoft **cabinet (`.cab`)** archives. The plugin
dialog's *Add* button extracts the `.cab` into `plugin\`. A `.cab` may contain a
single plugin folder or, as with XPE, an entire **set** of cooperating plugin
`.inf` files and their support files (see [`05-case-study-xpe.md`](05-case-study-xpe.md)).

## Vendor extension: `[Update]` (non-standard)

Some third-party plugins — notably Sherpya's XPE — add an `[Update]` section that
is **not** part of Bart's documented format. Base PE Builder does not require it;
it supports online version-checking and expresses inter-plugin relationships:

```ini
[Update]
id=xpe                                     ; this plugin's identifier
depends=ramdisk                            ; requires the 'ramdisk' plugin
url=http://winpe.sourceforge.net/plugin_check.php
Version=1.0.7
```

Observed keys:

| Key | Meaning |
|-----|---------|
| `id` | A stable identifier for the plugin. |
| `Version` | The plugin's version. |
| `url` | An update/version-check endpoint. |
| `depends` | Another plugin this one **requires** (e.g. XPE `depends=ramdisk`). |
| `parent` | Marks this plugin as a **child/customization** of another (e.g. the XPE customization plugin uses `parent=xpe`). |

This is the closest thing the ecosystem has to a **dependency / relationship
model**, and it is a per-vendor convention rather than a PE Builder feature.