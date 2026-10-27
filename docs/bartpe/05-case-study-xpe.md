# 05 — Case Study: Sherpya's Windows XPE Plugin

**Windows XPE** (by Gianluigi Tiesi, "Sherpya") is the best-known BartPE plugin.
Its name is *XP + PE = XPE*: it brings BartPE close to a real Windows XP desktop
by enabling the **Explorer shell**, taskbar/Start menu, DirectX, the Microsoft
Management Console, Windows Media Player, WMI, cryptography, and Plug-and-Play /
multimedia support.

This case study documents how XPE is put together, because it exercises nearly
every corner of the plugin format and established several ecosystem conventions.
Reference version: **XPE 1.0.7** (`xpe-1.0.7.cab`).

## It is a *bundle*, not a single plugin

Extracting `xpe-1.0.7.cab` yields a **set of cooperating plugin `.inf` files**
plus their support files:

| Plugin `.inf` | `[Update] id` | Role |
|---------------|---------------|------|
| `xpe.inf` | `xpe` | Main plugin — Explorer shell, logon, core files. |
| `xpe-crypto.inf` | `xpe-crypto` | Cryptography / protected-storage providers. |
| `xpe-defaults.inf` | `xpe-defaults` | Default settings. |
| `xpe-directx.inf` | *(directx)* | DirectX support. |
| `xpe-helpsys.inf` | `xpe-helpsys` | Help subsystem. |
| `xpe-mmc.inf` | `xpe-mmc` | Microsoft Management Console. |
| `xpe-pnp.inf` | `xpe-pnp` | Plug-and-Play & multimedia. |
| `xpe-wmi.inf` | `xpe-wmi` | Windows Management Instrumentation. |
| `xpe-wmp.inf` | `xpe-wmp` | Windows Media Player 9. |
| `z_xpe-custom.inf.sample` | `xpe-customizations` | **User customization template** (see below). |

Support binaries shipped in the bundle include `XPEinit.exe`, `XPELogon.exe`,
`PEGina.dll` (a custom GINA), `XPEShutDown.dll`, plus `oeminfo.ini` / `oemlogo.bmp`
and `winpe-xpsp2-fix.reg`.

Each sub-plugin is a valid standalone plugin (its own `[Version]` / `[PEBuilder]`)
that makes heavy use of `.2600` / `.3790` conditional sections so a single bundle
works against both XP and Server 2003 sources.

## Dependencies

XPE declares a hard dependency through the vendor `[Update]` extension:

```ini
[Update]
id=xpe
depends=ramdisk        ; requires the Nu2 RAMDisk plugin
```

The main plugin requires the **`ramdisk`** plugin (Nu2 Productions RAMDisk). In
practice XPE also expects supporting plugins documented by Sherpya —
**AutoRamDiskResizer**, **HWPnP**, and the **nu2xpe** menu converter — plus the
associated build scripts, to be fully functional.

## Shell & boot/init sequence

XPE replaces the bare Nu2Menu shell with **Explorer** and drives startup through
registry `RunOnceEx` entries that invoke its own init binary, `xpeinit.exe`. From
the customization template's `[Software.AddReg]`:

```ini
0x1,"Microsoft\Windows\CurrentVersion\RunOnceEx\A00","100","||xpeinit.exe -9"
0x1,"Microsoft\Windows\CurrentVersion\RunOnceEx\900","100",
    "||xpeinit.exe -f -m ""Detecting PnP Devices"" bartpe.exe -pnp -cn WinXPE -wg WORKGROUP"
```

- `xpeinit.exe` performs the XPE initialization (splash/progress, environment
  setup), and orchestrates PnP detection by calling `bartpe.exe -pnp` with the
  computer name (`-cn WinXPE`) and workgroup (`-wg WORKGROUP`).
- `XPELogon.exe` + `PEGina.dll` provide the logon path; `XPEShutDown.dll`
  provides shutdown.
- Like the base build, XPE sets the loader prompt via `[SetValue]` on
  `txtsetup.sif`:

  ```ini
  [SetValue]
  "txtsetup.sif","SetupData","loaderprompt","""Starting Windows XPE [The Horse Power]..."""
  ```

## The customization plugin (`z_xpe-custom`)

`z_xpe-custom.inf.sample` is shipped as a **template**; the user renames it to
`z_xpe-custom.inf` to activate it. It demonstrates the ecosystem's override
pattern precisely:

- The **`z_` prefix** makes it sort **last**, so it runs after `xpe` and all the
  sub-plugins and can override them.
- Its `[Update]` marks it a child of the main plugin:

  ```ini
  [Update]
  id=xpe-customizations
  parent=xpe
  ```

- It is otherwise an ordinary plugin containing `[SetValue]`, `[WinntDirectories]`,
  `[SourceDisksFiles]`, and the registry sections — i.e. it customizes the build
  by *layering additional directives*, never by editing the main XPE files.

Things the template lets a user change (all as plain registry/INI directives):

- **Computer name / workgroup** — `ComputerName` set to `WinXPE`.
- **Screen resolution / colour depth** — via `VgaSave` `DefaultSettings`
  (`BitsPerPel`, `XResolution`, `YResolution`).
- **Registered owner/organization**, locale/keyboard language.
- **Fonts**, desktop background, IE start page (`about:blank`), visible desktop
  icons, Explorer/console behaviour, and the shell wiring.
- The loader prompt text.

## The boot-fix / file-surgery pattern

XPE builds were notoriously sensitive: getting a given source to boot
successfully often required **adding a few files and removing/renaming others**
from the copy set. Because the customization plugin can carry its own
`[SourceDisksFiles]` (and, via `.BuildNo` variants, target XP vs 2003 separately),
the accepted way to apply such fixes was to edit the *customization* plugin's file
list — **not** the main XPE `.inf` files:

- add missing files → extra `[SourceDisksFiles]` entries;
- drop problematic files → remove/adjust their entries;
- keep everything reproducible in one late-loading plugin.

This is the same layering principle as `[Update] parent=`/`depends=` and the
`z_`-prefix ordering convention described in
[`04-plugin-conventions.md`](04-plugin-conventions.md): the base and vendor
plugins stay pristine, and all site-specific corrections live in a single
last-loaded override plugin.

## Why XPE is the key stress test

XPE exercises, in one bundle:

- multi-file plugin **bundles** with inter-plugin **dependencies**;
- exhaustive `.2600` / `.3790` **conditional builds**;
- every registry hive target (`Software` / `SetupReg` / `Default`);
- `[SetValue]` INI patching (`txtsetup.sif`);
- shipped runtime binaries with the *pluginCheck* attribute;
- the `z_`-prefix **override/customization** convention;
- the vendor `[Update]` **relationship model**.

Anything that can faithfully process XPE can process essentially the whole BartPE
plugin ecosystem.