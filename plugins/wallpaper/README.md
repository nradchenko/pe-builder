<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# wallpaper

A desktop wallpaper for the Explorer desktop, which otherwise renders on a flat background
colour.

The plugin ships no code and applies nothing: it stages the source's own image, re-encoded as a
bitmap, and declares three registry values. Windows does the rest — `userinit` reads those values
at logon and paints the desktop before the shell even starts.

## What it stages

One file: `bliss.jpg`, the XP default wallpaper, a normal declared file on the installation media
(stored compressed as `BLISS.JP_`, which the engine resolves from the bare name). It is staged as
`bliss.bmp` — attribute 64 re-encodes it on the way in; see § Why a bitmap.

About 470 KB, which is the whole cost of the plugin.

## Why system32 and not Web\Wallpaper

A real XP install keeps wallpapers in `%SystemRoot%\Web\Wallpaper`. A PE has no such directory,
and creating one would buy nothing: the media's own `layout.inf` already declares `bliss.jpg`'s
target as system32, which is the flat directory a PE runs from. Staging it where the source says
it goes keeps the plugin to a single `[SourceDisksFiles]` line, with no `[WinntDirectories]`
entry — the only field it sets beyond the directory is the name, since the staged file is a
bitmap and should say so.

## Why a bitmap

The path that paints a desktop wallpaper — `SystemParametersInfo(SPI_SETDESKWALLPAPER)` — draws
**bitmaps only**, and `bliss.jpg` is a JPEG. That is the whole of it.

Active Desktop decodes a JPEG, but it cannot be used: **XP Professional x64 has no Active Desktop
at all** — the feature is absent, not disabled. Its registry key is never created and Display
Properties has no Web tab. Handed a non-BMP image, Windows converts it to a bitmap and applies
that classically, on every edition.

Converting at build time is that same conversion, moved earlier — the decoder is Go's rather than
something the image has to carry, and one path serves both architectures.

The engine converts to a 256-colour bitmap; `internal/tree/bitmap.go` records why that palette
and not another, with the measurements.

## The registry values are the whole contract

`userinit.exe` runs from winlogon before the shell and applies `Control Panel\Desktop` itself, so
this plugin declares the values and stops there. Nothing applies them on its behalf, and the
desktop is already painted by the time the shell's splash appears.

## Fit

`WallpaperStyle = 2` (stretch) with `TileWallpaper = 0`. The desktop resolution is a build-time
choice made on `explorer-shell`, so the image has no fixed canvas to match; stretching scales it
to whatever mode is selected rather than leaving a border around it. Centring (`0`) and tiling
(`1`) are the other two values, read by the shell straight from the registry.

`Wallpaper` is written as **REG_EXPAND_SZ**: the PE's system drive is `X:`, not `C:`, so an
absolute path baked in at build time would point nowhere at run time.

## Known limitation

The Display Properties preview thumbnail does not render, though applying a wallpaper from that
dialog works. Cosmetic, and not worth the components it would take to fix.
