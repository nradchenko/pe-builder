<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# luna

The Windows XP **"Luna"** visual style for the Explorer desktop, which otherwise renders in the
classic (Windows 2000) look. Off by default; enabling it auto-pulls **explorer-shell** (the desktop
it themes, and the applier) and **terminal-services** (the console session the theme engine needs —
a PE has none).

The plugin **ships no code and needs no toolchain**: it stages the visual style and the Themes
service, and selects the style in the registry. `peshell` (from explorer-shell) applies whatever is
selected. Another visual style would be this file with different values.

## Pieces

| File | What it is |
|---|---|
| `luna.inf` | the whole plugin: staging, the Themes service, and the style selection |

Everything else already exists: `uxtheme.dll` (the in-process theme engine) comes from
explorer-shell, and the session from terminal-services.

## What it takes to theme a PE

Three things, and only the third is this plugin's own:

1. **A console session** — the theme server serves sessions, and a PE has none. That is the
   `pesuite` → `terminal-services` chain; see that plugin's README.
2. **The Themes service** — `shsvcs.dll` hosted by svchost (`ThemeServiceMain`). `hivesys.inf` does
   not carry it, so `[SetupReg.AddReg]` defines it here: a Win32 service (`Type=0x20`) in its own
   svchost group, `Start=0x2` (auto), plus a `Setup\AllowStart\Themes` entry — the setup-mode SCM
   only auto-starts services on that whitelist.
3. **A style, selected** — the `.msstyles` plus the registry values below.

## The contract with peshell

`[Default.AddReg]` writes four values under
`Software\Microsoft\Windows\CurrentVersion\ThemeManager`, and peshell reads exactly those at shell
start and applies the style they name. **How and why the apply works the way it does is
explorer-shell's business — see its README § Visual style.**

| Value | Meaning |
|---|---|
| `ThemeActive` | `"1"` turns theming on; anything else and peshell does nothing |
| `DllName` | the `.msstyles`, a **`REG_EXPAND_SZ`** so it resolves against the real boot drive |
| `ColorName` | the colour scheme (below) |
| `SizeName` | the font/metric size (below) |

They go in the **DEFAULT** hive because the PE runs as SYSTEM with no profile loaded, so `HKCU`
*is* `HKU\.DEFAULT`.

`DllName` must stay `REG_EXPAND_SZ`: this PE boots from CD and **its system drive is not `C:`**.
peshell expands it; nothing may rebuild that path by hand.

## Colour schemes and sizes

`Luna.msstyles` carries **all three** of XP's colour schemes and **all three** sizes, and every
combination works. Blue at normal size is the default because that is XP's, not because the
others are unavailable — pick another with the `color` and `size` options:

| `color` → `ColorName` | Scheme |
|---|---|
| `NormalColor` | blue (XP's default — this plugin's default) |
| `HomeStead` | olive |
| `Metallic` | silver |

| `size` → `SizeName` | Size |
|---|---|
| `NormalSize` | normal (this plugin's default) |
| `LargeFonts` | large |
| `ExtraLargeFonts` | extra large |

```hcl
plugin "luna" {
  enabled = true
  options {
    color = "HomeStead"
    size  = "LargeFonts"
  }
}
```

Note the size value is `ExtraLargeFonts`, not `ExtraLarge`: the msstyles names each
size/colour *combination* internally by concatenation (`ExtraLargeHomeStead`), and that bare
`ExtraLarge` prefix is not what `SizeName` takes.

Both are also rewritten at run time by Display Properties → Appearance, if the desktop has it.

## Fonts

Luna draws window captions in **Trebuchet MS Bold** (everything else it uses is Tahoma, which the
base layer stages), so `[SourceDisksFiles]` stages `trebuc.ttf`/`trebucbd.ttf` into the Fonts
directory. Only the files are needed — the SOFTWARE hive already registers them by name. Without
them the caption falls back to a font with no glyphs for the source's language and the title draws
as empty rectangles at the larger sizes.

## Adding another visual style

Copy this file, change the staged `.msstyles` and the four `ThemeManager` values. No code, no
`[Build]`. Only a style needing *different machinery* would need more.

## Known gaps

- Display Properties' Themes tab calls the current settings a **modified theme**: they do not match
  a saved `.theme` file, because sound bindings are deliberately removed (they once hung the shell
  in `waveOut`) and no wallpaper is staged. Cosmetic, and arguably accurate.

## If the theme renders but controls look classic

That symptom looks like luna's and is not: **it is a comctl32 v6 side-by-side binding problem, and
it belongs to win32-gui.** Staging a visual style themes the window frame; the tab controls and
buttons *inside* a window are drawn by comctl32 v6, which a process only gets by asking for it —
and the loader honours that request only from a manifest at resource id 1, which `rundll32.exe`
(the host of every Control Panel applet) does not have and `explorer.exe` carries at id 123. Both
are given one by win32-gui; see its `[SourceDisksFiles]` and the manifests beside it. Start there,
not here.