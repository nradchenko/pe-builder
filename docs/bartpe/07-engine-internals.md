# 07 — PE Builder: Engine Internals (from binary analysis)

The other documents describe PE Builder from its own help and data files. This
one records what was learned from **static analysis of the binaries themselves** —
`pebuilder.exe` and `bartpe.exe` from PE Builder 3.1.10a.

> **Provenance / confidence.** Most findings come from unpacking the UPX-compressed
> binaries and examining their **imports, embedded strings, and resources**; a few
> (noted inline) come from **targeted radare2 disassembly**, and **binwalk**
> confirmed there is no embedded or appended data in either binary. A few findings
> (noted inline) are **measurements against a real Windows XP SP3 x86 source** rather
> than from the binaries. Facts about
> *which* Windows APIs are used and *what* literal strings/commands exist are
> high-confidence. Control-flow details traced by hand are flagged as such. Items
> still requiring deeper reverse engineering are listed at the end.

## Build toolchain

- **`pebuilder.exe`** — PE32 (GUI), Intel 80386, **UPX-packed**. Underneath: an
  **MFC application built with MSVC 6.0** (linker version 6.0; the resource string
  table is largely MFC boilerplate). 32-bit **i386 only** — there is no x64 build.
- **`bartpe.exe`** — PE32 (console), i386, UPX-packed. A small runtime helper
  (see below).
- Neither binary has an overlay or appended/embedded archive; resources are only
  icons, cursors, bitmaps, dialogs, a menu, and the string/version tables.
- Product version resource: `3,1,2,0`; copyright *2002-2006 Bart Lagerweij*.

## The INF engine is Windows **setupapi**

`pebuilder.exe` imports and uses the Windows **setupapi** INF parser directly:

```
SetupOpenInfFileA      SetupFindFirstLineA     SetupFindNextLine
SetupCloseInfFile      SetupFindNextMatchLineA SetupGetLineTextA
SetupGetFieldCount     SetupGetStringFieldA    SetupGetIntField
SetupGetBinaryField    SetupGetLineCountA
```

**Implication:** the plugin `.inf` format *is* the standard Windows setupapi INF
format — which is exactly why every plugin must declare
`[Version] Signature="$Windows NT$"` (without it, `SetupOpenInfFile` refuses the
file). A faithful reimplementation of the format must therefore implement
**setupapi INF semantics**, not a naïve INI parse:

- Lines are split into **fields** by commas; fields may be double-quoted (and
  embedded quotes are doubled `""`).
- A `[Strings]` section provides `%TOKEN%` substitutions expanded across the file.
- Values are read positionally with `SetupGetStringField` / `SetupGetIntField` /
  `SetupGetBinaryField` — e.g. the AddReg line `0x1,"key","name","data"` is field 0
  = type (int), field 1 = subkey (string), etc. The `%%…%%` doubling seen in the
  base manifest's network edits is INF string escaping.
- Line continuation with a trailing `\` is supported by setupapi.

## File copying & decompression

- Files are copied/decompressed with **`SetupDecompressOrCopyFileA`** — the single
  setupapi call that transparently decompresses an SZDD/MSZIP-compressed source
  file (`*.??_`) or copies it verbatim. This is the mechanism behind
  `[SourceDisksFiles]` (and the `attribute=2` "do not decompress" case is simply
  *copy* instead of *decompress*).
- Cabinet (`.cab`) contents are enumerated/extracted with
  **`SetupIterateCabinetA`** (used for the plugin *Add* button, and for
  cab-packed source files).

The engine also reads the Windows source's own layout INFs — **`layout.inf`** (the
master file → directory / compressed-name manifest), **`drvindex.inf`**, and
**`biosinfo.inf`** — which it consults to resolve where source files live and their
compressed names. (These strings are referenced in the binary; the precise usage is
inferred.)

On a non-Windows host the decompression is replaceable with an SZDD/MSZIP/LZX cab
decompressor such as libmspack / `cabextract`.

> **Measured (real Windows XP SP3 x86 source):** essentially every compressed file —
> the loose single-file `.??_` cabinets *and* the multi-file `DRIVER.CAB` / `SP3.CAB`
> — uses **LZX** (`LZX:21`); MSZIP and SZDD/KWAJ were not observed. A reimplementation
> targeting XP SP3 needs LZX above all.

## File selection (which files are copied)

`pebuilder.inf` is **not** a full base file manifest — its `[SourceDisksFiles]`
lists only ~58 files (kernel/HAL variants, a handful of utilities and apps, and a
few BartPE-own files) alongside the registry and boot edits. The bulk of the OS
base is selected from the Windows source itself:

- **Boot drivers** — from `txtsetup.sif`'s driver *load* sections, which the engine
  references (`[Hal.Load]`, `[DiskDrivers.Load]`, `[FileSystems.Load]`, `[SCSI.Load]`,
  `[BootBusExtenders.Load]`, `[BusExtenders.Load]`, `[InputDevicesSupport.Load]`,
  `[Keyboard.Load]`, `[Display.Load]`) — i.e. the drivers a minimal NT boot loads.
- **File locations / compressed names / directory IDs** — resolved via the source's
  `layout.inf` and `txtsetup.sif` (whose `[WinntDirectories]` carries the full
  directory-ID → path table, 100+ entries).
- **Core Win32 subsystem** — a fixed list built into `pebuilder.exe` (core Win32
  filenames appear as embedded strings in the binary).

The result is a **curated subset** of the source, not its full file set (a bare
build is ~160 MB). *(The exact subset was not enumerated from static analysis; the
mechanism is inferred from the `*.Load` / `layout.inf` references in the binary and
from comparing `pebuilder.inf` against a real source's `txtsetup.sif`.)*

## Registry / hive building

PE Builder v3 does **not** hand-write registry hive (`regf`) files. It builds them
through the live Windows registry API (ADVAPI32):

```
RegLoadKey  RegUnLoadKey  RegSaveKey  RegFlushKey
RegCreateKey  RegDeleteKey  RegSetValue  RegDeleteValue
RegOpenKey  RegEnumKey  RegEnumValue  RegQueryInfoKey  RegQueryValue
```

Observed method (log strings: `openHive()`, `closeHive()`,
`RegSetValue value=… type=0x%lX`, `RegCreateKey hive=… key=…`):

1. Start from base hive material taken from the **Windows source**:
   - `setupreg.hiv` → the **SYSTEM** hive base,
   - `hivesft.inf` → **SOFTWARE**,
   - `hivedef.inf` → **DEFAULT** (user),
   - `hivecls.inf` → **CLASSES**.
   (`hive*.inf` are the source's own setupapi hive-definition INFs.)
2. Load a hive with `RegLoadKey` under a temporary mount point (temp hives named
   `petmphive` / `setuphiv`, with `.log` files).
3. Apply the merged `AddReg` / `DelReg` directives (base manifest + all enabled
   plugins) via `RegSetValue` / `RegDeleteKey` / `RegDeleteValue`.
4. Persist with `RegSaveKey` and unload.

On a non-Windows host the *result* is replicable with a hive library such as
hivex (which can read and write `regf` directly).

## Source validation

The source is validated by reading a file's version resource
(`GetFileVersionInfo`, VERSION.dll) and comparing:

- Checked file: **`i386\ntdll.dll`** (with `NTOSKRNL.EXE` also referenced).
- Required version: **`5.1.2600.1045` or higher** — i.e. **Windows XP SP1**.
- Error strings: *"File version … should be 5.1.2600.1045 or higher."*,
  *"Source files wrong version."*, *"Need Windows 2000/XP/2003 to run this program…"*

`txtsetup.sif` and `txtsetup.oem` are also read (the latter for storage-driver
integration).

## ISO mastering & boot media

External tools are launched via `CreateProcessA`:

- **`mkisofs.exe`** builds the ISO; **`cdrecord.exe`** burns CD; **`StarBurn.dll`**
  (imported directly) handles CD/DVD burning including DVD.

The `mkisofs` command line is assembled from these literal fragments:

```
mkisofs -iso-level 4 -volid "<label>" ... -o "<output.iso>" \
        -b bootsect.bin -no-emul-boot -boot-load-size 4 \
        -hide bootsect.bin -hide boot.catalog -duplicates-once
```

Key points:

- **`-iso-level 4`** (allows long ISO 9660 names) with **no `-J` (Joliet) and no
  `-R` (Rock Ridge)** — long names come from level 4, and on-disc names are
  uppercased (the `[SourceDisksFiles]` `attribute=8` flag).
- El Torito is **no-emulation**, boot image `bootsect.bin`, load size **4**
  (4 × 512 = 2048 bytes); both `bootsect.bin` and `boot.catalog` are hidden.
- **Confirmed argument order** (from disassembling the command-builder
  `fcn.004019e9`): `-iso-level 4 -volid "<label>"`, then the El Torito block
  `-b bootsect.bin -no-emul-boot -boot-load-size 4 -hide bootsect.bin -hide
  boot.catalog`, then `-o "<output.iso>"`, then the **source directory** as the
  final positional argument (with `-duplicates-once` among the options).

### CD-boot constraints the ISO must satisfy

The XP real-mode boot chain imposes two ISO 9660 requirements on any bootable disc,
including the ones PE Builder produces. Both are **measured against real Microsoft XP
media**, which satisfy them:

- **Version-less filenames.** Microsoft's mastered media carry bare names
  (`SETUPLDR.BIN`), *not* the classic ISO 9660 `;1` version suffix. XP's **CDBOOT**
  boot sector matches `\I386\SETUPLDR.BIN` byte-for-byte and **fails if the `;1`
  suffix is present**. Level 4 (`-iso-level 4`, ISO 9660:1999) is the relaxation
  under which version-less names are conformant; whether the original `mkisofs` line
  omits the suffix through level 4 alone or via an explicit omit-version flag (`-N` /
  `-omit-version-number`) was not determined from the binary.
- **§9.3-sorted directory records.** XP's real-mode **`setupldr`** ISO 9660 reader
  locates a file only if the containing directory's records are in ISO 9660 §9.3
  order (sorted by file identifier). CDBOOT itself linear-scans and is
  order-independent, so a disc with unsorted records *begins* to boot but then
  `setupldr` cannot find `\I386\NTDETECT.COM`, producing the characteristic NTDETECT
  reboot loop.

### Boot files

- `SETUPLDR.BIN`, `NTDETECT.COM`, and `BOOTFIX.BIN` are copied from the source
  (uppercased for CD).
- **No evidence of `setupldr.bin` binary patching** was found in the strings —
  consistent with PE mode being driven entirely by the `/minint` edit to
  `txtsetup.sif` (which lives in `pebuilder.inf`, not the executable). The strings
  `minint`, `osloadoptions`, and `loaderprompt` do **not** appear in the exe,
  confirming that behaviour is data-driven, not hardcoded.
- **`bootsect.bin` is generated at build time by `pebuilder.exe`.** It is *not*
  shipped with PE Builder and *not* embedded in the executable — confirmed by
  binwalk, a full extracted-tree file search, and the absence of a
  `0x55AA`-terminated record in `.data`. It is written into the output as
  `<output>\bootsect.bin` by the boot-file routine (`fcn.0040e258` →
  `fcn.0040ba30`, which reads source boot files via
  `CreateFile`/`GetFileSize`/`ReadFile`). The `changes.txt` history corroborates
  the CD boot spec *`[Boot] Image file=…\bootsect.bin, kind of emulation=no`*. The
  exact byte-generation algorithm would need a full decompile; the practical route
  to the bytes is to extract `bootsect.bin` from any ISO the original tool built.
  (The separate `peinst` hard-disk installer uses `mkbt.exe` to write a `peboot.bin`
  boot sector — a different path from the CD `bootsect.bin`.)

## `bartpe.exe` — the runtime network/PnP configurator

`bartpe.exe` identifies itself as **"Bart's Network Configurator (bnetcfg.exe)"**.
It runs inside the booted environment (invoked from `RunOnceEx`) to bring up
networking and Plug-and-Play:

| Switch | Meaning |
|--------|---------|
| `-cn <name>` | Set computer name (`*` is replaced automatically). |
| `-wg <workgroup>` | Set workgroup. |
| `-pnp` | Install drivers using PnP. |
| `-minint` | Shorthand for `-pnp -cn bartpe-* -wg workgroup`. |
| `-v` | Verbose. |

It manages Network Adapters / Clients / Protocols / Services and reads
`SYSTEM\CurrentControlSet\Control\Session Manager\WPA\PnP`. This is exactly the
`bartpe.exe -pnp -cn WinXPE -wg WORKGROUP` call the XPE plugin issues from
`RunOnceEx` (see [`05-case-study-xpe.md`](05-case-study-xpe.md)).

## Section vocabulary — confirmed complete

The engine's recognised section/directive tokens, as embedded literal strings, are
**exactly** the set documented in [`03-plugin-format.md`](03-plugin-format.md):
`PEBuilder`, `WinntDirectories`, `SourceDisksFiles`, `SourceDisksFolders`,
`Software.AddReg`/`.DelReg`, `SetupReg.AddReg`, `Default.AddReg`/`.DelReg`,
`SetValue`, `AddLine`, `DelLine`, `Append`. **No undocumented sections were
found.** (The vendor `[Update]` section used by XPE is *not* referenced by the
engine — it is ignored by base PE Builder, consistent with it being a third-party
convention.)

The engine also uses **`@NN@` numeric placeholders** that expand to the path of a
directory ID (e.g. references to IDs `56`, `61`, `77`, `99`, `122`, `152`, `199`,
`252`, `255`, `256` appear). This confirms the real directory-ID table is
substantially larger than the "most-used" subset the help documents.

## Open items (would require deeper RE)

Not obtainable from strings/imports/resources alone; would need disassembly
(e.g. radare2) or empirical extraction from a real build:

1. ~~The complete directory-ID → path table~~ — **resolved**: the Windows source's
   own `txtsetup.sif` carries the full table in its `[WinntDirectories]` section
   (100+ entries), so it is read from the source rather than the binary.
2. ~~The exact `mkisofs` argument order~~ — **resolved** (see the ISO section above).
3. ~~`bootsect.bin` origin~~ — **resolved**: generated at build time from the source
   boot files; only the exact byte algorithm remains, best obtained from a built ISO.
4. The precise **hive-merge ordering** when base-manifest and plugin `AddReg`
   directives touch the same key/value — *still open* (would need decompilation of
   the AddReg iteration loop).
