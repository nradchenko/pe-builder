# 01 — Overview

## What it is

**PE Builder** is a freeware Windows application by **Bart Lagerweij** (nu2.nu)
that builds **BartPE** — "Bart's Preinstalled Environment" — a bootable, live
Windows environment that runs entirely from removable/read-only media (CD, DVD,
or USB flash disk) or from RAM.

BartPE is a community-built counterpart to Microsoft's WinPE. It boots a
stripped-down Windows XP / Server 2003 kernel and shell without touching or
requiring an installed operating system, which makes it useful for recovery,
diagnostics, virus scanning, imaging, and data rescue.

Key characteristics:

- The environment is **read-only**: the registry is loaded into RAM at boot and
  no changes are written back to the boot media.
- Each boot is effectively a fresh, minimal Windows installation running in
  "mini-setup" mode (see the `/minint` boot option in
  [`02-build-process.md`](02-build-process.md)).
- The base (bare) build boots to a simple shell; **plugins** add drivers,
  applications, and alternative shells (see [`03-plugin-format.md`](03-plugin-format.md)).

## Versions

- Product line: **PE Builder v3**.
- Reference version for this documentation: **3.1.10a**, released **2006-02-17**
  (the last widely distributed release).
- Copyright shown throughout the product: *Copyright (c) 2002-2006 Bart
  Lagerweij. All rights reserved.*

The project was subsequently discontinued; the nu2.nu site and downloads are now
only available via web archives.

## What it produces

Depending on the selected media output, a build yields one of:

1. **A file tree only** — the assembled `BartPE` output directory (no image).
2. **A bootable ISO image** — an El Torito bootable CD/DVD image (built with
   `mkisofs`).
3. **Burned optical media** — the build written directly to a CD/DVD (via
   `cdrecord` or the StarBurn SDK).
4. **A bootable USB flash disk** — via the separate `pe2usb` helper (uses a RAM
   disk boot method; see [`02-build-process.md`](02-build-process.md)).

A bare BartPE CD is about ~160 MB; a full desktop build using the XPE plugin is
around ~220 MB.

## Requirements

### Host (build machine)

- A 32-bit Windows host to run `pebuilder.exe` (Windows 2000 / XP / Server 2003,
  and BartPE itself).

### Source media (supplied by the user)

BartPE is **not** distributed with any Microsoft files. The builder condenses
files from a genuine Windows installation source that the user provides. Accepted
sources (per the main dialog help):

- Windows XP Home Edition — **Service Pack 1 or higher**
- Windows XP Professional — **Service Pack 1 or higher**
- Windows Server 2003 — Web / Standard / Enterprise Edition

The source is identified internally by its **build number**: `2600` for Windows
XP and `3790` for Server 2003 (used by conditional plugin sections — see
[`03-plugin-format.md`](03-plugin-format.md)).

PE Builder validates the source by reading the version of `i386\ntdll.dll`, which
must be **`5.1.2600.1045` (Windows XP SP1) or higher** — see
[`07-engine-internals.md`](07-engine-internals.md).

If no installation CD is available, the setup files may still exist on disk; PE
Builder provides a *Source → Search* option to locate them, and a **slipstream**
dialog to fold a newer service pack into the source.

## Bundled third-party tools

| Tool | Purpose | License / origin |
|------|---------|------------------|
| `mkisofs.exe` | Build the ISO image | GNU GPL — Jörg Schilling |
| `cdrecord.exe` | Burn CD | GNU GPL — Jörg Schilling |
| `StarBurn.dll` | Burn CD/DVD (DVD burning path) | StarBurn SDK — Rocket Division Software |
| `pe2usb.cmd` / `pe2usb.bin` | Install/boot from USB flash disk | Bart Lagerweij |

Note: burning with `cdrecord` supports CD only; DVD burning requires the StarBurn
path.

## Licensing of PE Builder itself

`pebuilder.exe`, `bartpe.exe`, `netconfig.exe`, and `scangui.exe` are covered by
the **PE Builder license** (freeware, source never released). Salient terms:

- Free of charge to **use, copy, publish, and distribute**.
- **May not be sold**, and no fee may be charged for the software itself (a fee
  may be charged only for the media/transfer).
- **May not be modified**; must be distributed only in its **original archive**,
  unchanged (adding or removing files from the archive is prohibited).
- **May not be incorporated** into any other software without written permission.
- Provided "as is", no warranty.

These terms are why the tool cannot simply be extended or embedded, and — with no
source available — why understanding it requires the kind of reverse-engineering
recorded in this reference.

## Related components

- **`bartpe.exe`** — a runtime helper included on the built environment (invoked
  during boot/PnP initialization, e.g. by the XPE plugin).
- **`netconfig.exe`** — network configuration helper.
- **`scangui.exe`** — a GUI helper.
- **Nu2Menu** — the default lightweight menu/shell used by a bare build.