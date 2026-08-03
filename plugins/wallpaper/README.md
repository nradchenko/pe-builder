<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# wallpaper

A desktop wallpaper for the Explorer desktop, which otherwise renders on a flat background
colour.

The plugin itself ships no code: it stages an image and the Active Desktop surface that paints
it, then declares three things in the registry — the wallpaper to use, the COM server to
register, and the policy to lift. `peshell` (from `explorer-shell`) does the applying.

## What it stages

`bliss.jpg` — the XP default wallpaper — plus `mshtml.dll` and `mshtml.tlb`, the HTML engine and
its type library, which are what the Active Desktop surface is. All three are normal declared
files on the installation media, stored compressed under 8.3 names (`BLISS.JP_`, `MSHTML.DL_`),
which the engine resolves from the bare name.

That costs about 4.2 MiB on the ISO, which is why this is a separate plugin rather than part of
the desktop: a build that does not enable it stages none of it and pays nothing.

The set is minimal by test, not by guess — each file was removed in turn and the result booted.
`mshtml.tlb` is required (without it the desktop comes up bare, so the type library matters even
though the apply is a direct interface call). IE's image decoder `imgutil.dll` was in the first
working build but is **not** required — mshtml decodes this wallpaper by itself — so it was
dropped. If a future image format needs decoding mshtml can't do, that is where to look first.

## Why system32 and not Web\Wallpaper

A real XP install keeps wallpapers in `%SystemRoot%\Web\Wallpaper`. A PE has no such directory,
and creating one would buy nothing: the media's own `layout.inf` already declares `bliss.jpg`'s
target as system32, which is the flat directory a PE runs from. Staging it where the source says
it goes keeps the plugin to one `[SourceDisksFiles]` line per file, with no `[WinntDirectories]`
entry and no rename.

## Why Active Desktop

The classic desktop path paints **bitmaps only**. `bliss.jpg` is a JPEG, so it can only be drawn
by the Active Desktop surface, which decodes it through the imaging codecs.

That surface is off in a plain desktop build, deliberately: `explorer-shell` sets
`NoActiveDesktop=1` because the surface needs an HTML engine and folder `.htt` templates a
minimal PE does not stage. This plugin stages the engine, so it lifts the policy for itself —
its `[Default.AddReg]` runs after the desktop's, which a deps-first plugin order guarantees.

Two consequences worth knowing:

- **mshtml has to be registered.** Nothing on the media registers it and the baked hive carries
  no CLSID for it, so it is staged but inert. The plugin declares it under
  `pe-builder\SelfRegister` and `peshell` registers it (see that plugin's README §
  Self-registration). Registration is also why `mshtml.tlb` has to be staged: the type library
  is registered along with the classes.
- **The error for an unregistered mshtml names the wrong thing.** The shell reports that it
  cannot find the *Active Desktop HTML file*, pointing at a missing `.htt` when the real problem
  is a COM class it could not create. The `.htt` is generated on demand and is not a media file;
  do not go looking for one.

## Why the registry alone is not enough

Setting `Control Panel\Desktop\Wallpaper` does not produce a wallpaper — nothing in a PE reads
it. `peshell` is what applies it (see `explorer-shell`'s README § Wallpaper for how and why),
so this plugin only declares the values.

Verified by ablation: with every other ingredient in place and only that apply removed, the
desktop comes up on a flat colour.

## Fit

`WallpaperStyle = 2` (stretch) with `TileWallpaper = 0`. The desktop resolution is a build-time
choice made on `explorer-shell`, so the image has no fixed canvas to match; stretching scales it
to whatever mode is selected rather than leaving a border around it. Centring (`0`) and tiling
(`1`) are the other two values, and `peshell` maps all three onto the Active Desktop API, whose
encoding differs from the registry's.

`Wallpaper` is written as **REG_EXPAND_SZ**: the PE's system drive is `X:`, not `C:`, so an
absolute path baked in at build time would point nowhere at run time.

## Known limitation

The Display Properties preview thumbnail does not render, though applying a wallpaper from that
dialog works. Cosmetic, and not worth the components it would take to fix.
