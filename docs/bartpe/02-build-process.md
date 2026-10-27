# 02 — PE Builder: The Build Process

This document describes how `pebuilder.exe` turns a Windows installation source
into a bootable BartPE environment, and how that environment boots.

## Inputs

Configured through the main dialog (see also the GUI/CLI sections below):

- **Source path** — a drive or local/remote path containing the Windows source
  files (the `i386` folder). Must be XP SP1+ or Server 2003 (see
  [`01-overview.md`](01-overview.md)).
- **Custom directory** — an optional local/remote path of extra files/folders to
  add verbatim to the output. (The help warns not to point this at the running
  Windows directory, and that a CD build has room for only ~300–400 MB of extras.)
- **Output directory** — where the assembled files are written. By default this
  path is **relative to the pebuilder directory**; an "Absolute output path"
  option allows an absolute path.
- **Media output** — one of *None* / *Create ISO image* / *Burn to CD/DVD*.
- **Plugins** — the enabled set (see [`03-plugin-format.md`](03-plugin-format.md)).
- **Drivers** — anything placed under the `drivers\` tree (see below).

## The base manifest: `pebuilder.inf`

The build is driven by a **base manifest**, `pebuilder.inf`, which uses exactly
the same section grammar as plugins (documented in
[`03-plugin-format.md`](03-plugin-format.md)). It defines the core set of files to
copy from the source, the base registry contents for the SYSTEM / SOFTWARE /
`.DEFAULT` hives, and the essential boot configuration edits. Conceptually it is
the **first/base layer**; enabled plugins are layered on top of it.

## Build phases

Roughly, a build proceeds through these stages (two of them can be independently
skipped — see Options/CLI):

1. **Read the source.** Locate and validate the Windows source; determine its
   build number (`2600` XP / `3790` Server 2003), which selects `.BuildNo`
   conditional sections.

2. **Build files.** Copy files into the output tree per `pebuilder.inf` and every
   enabled plugin's `[SourceDisksFiles]` / `[SourceDisksFolders]` sections,
   creating directories per `[WinntDirectories]`. Compressed source files
   (`*.??_`, e.g. `setupreg.hi_`, `ntdll.dl_`) are **decompressed** (via the
   Windows setupapi `SetupDecompressOrCopyFile` function) unless the file's
   attribute says otherwise. The *Custom directory* contents are added here.
   (Skippable — see *Don't build files* / `-skipfiles`.)

   Note that `pebuilder.inf` does **not** enumerate the full OS file set — it lists
   only a small supplement plus the registry. The bulk is selected from the Windows
   source itself (boot drivers from `txtsetup.sif`, locations from `layout.inf`, and
   a core Win32 list built into `pebuilder.exe`); see *File selection* in
   [`07-engine-internals.md`](07-engine-internals.md). The result is a curated
   subset of the source, not its full contents.

3. **Build the registry.** Assemble the BartPE registry hives (SYSTEM, SOFTWARE,
   and the default user hive) from a base hive plus the merged
   `[Software.AddReg]` / `[SetupReg.AddReg]` / `[Default.AddReg]` (and matching
   `.DelReg`) directives of the base manifest and all enabled plugins. The base hive
   content comes from the Windows source's own hive-definition files —
   `setupreg.hiv` (SYSTEM), `hivesft.inf` (SOFTWARE), `hivedef.inf` (DEFAULT user),
   and `hivecls.inf` (CLASSES) — and PE Builder v3 assembles the hives through the
   Windows registry API (`RegLoadKey` / `RegSetValue` / `RegSaveKey`) rather than a
   from-scratch `regf` writer (see
   [`07-engine-internals.md`](07-engine-internals.md)). (Skippable — see *Don't
   build registry* / `-skipregistry`.)

4. **Apply file/INI edits.** Apply `[SetValue]`, `[AddLine]`, `[DelLine]`, and
   `[Append]` directives, which patch text/INI files in the output — most
   importantly `txtsetup.sif` (boot options, below) and various Windows setup
   `.inf` files, and `nu2menu.xml` for the menu shell.

5. **Media output.** Depending on the selection: do nothing further (*None*),
   build an ISO with `mkisofs`, or burn to CD/DVD (`cdrecord` / StarBurn). The
   `-buildiso` mode rebuilds just the ISO from an existing output tree. The exact
   `mkisofs` invocation (El Torito no-emulation, `-iso-level 4`, no Joliet) is
   given in [`07-engine-internals.md`](07-engine-internals.md).

The process return value (errorlevel) is the **number of errors** encountered.

## Boot configuration

The single most important boot edit lives in the base manifest and targets
`txtsetup.sif`:

```ini
[SetValue]
; Adjust txtsetup.sif
"txtsetup.sif", "SetupData", "osloadoptions", """/fastdetect /minint"""
"txtsetup.sif", "SetupData", "loaderprompt", """Starting BartPE..."""
```

- **`/minint`** is the key flag. It puts the NT loader/kernel into the minimal,
  RAM-based "preinstall" mode used by WinPE-class environments: the system runs
  from the boot media / RAM rather than an installed disk, and the registry is
  volatile. This is what makes the result a *PE* rather than a normal install.
- **`/fastdetect`** disables serial-mouse detection on COM ports.
- **`loaderprompt`** is the text shown by the loader ("Starting BartPE...").
  Plugins may override it (XPE sets its own prompt via the same `[SetValue]`
  mechanism).

### The base registry overlay that makes it a PE

Beyond the `txtsetup.sif` edit, `pebuilder.inf`'s registry sections establish the
handful of keys that turn an otherwise normal NT boot into a read-only PE (in the
SYSTEM hive unless noted):

- **`ControlSet001\Control\MiniNT`** — a marker key that identifies the environment
  as a PE; many Windows components and applications key their behaviour off its
  presence.
- **`Setup\SetupType`** (non-zero) with **`Setup\CmdLine`** — put the boot into the
  "setup is in progress" path, so that **`winlogon.exe` runs `Setup\CmdLine` instead
  of showing the interactive logon UI** (see the boot chain below). This is the
  XP-era mechanism by which BartPE launches its shell — there is no `winpeshl`
  (that is Vista+ WinPE).
- **`Control\Session Manager\BootExecute`** set empty — skips the `autochk`/`chkdsk`
  boot pass, which a volatile, read-only PE cannot usefully run.
- **Security services** — the source's `setupreg.hiv` is minimal, so the base
  manifest hand-authors the LSA/security stack the boot assumes: the `Control\Lsa`
  package list (`kerberos`, `msv1_0`, `schannel`), the lsass-hosted `SamSs` and
  `ProtectedStorage` services and their `RpcSs` dependency, a `Setup\AllowStart`
  service whitelist, and `Control\SecurityProviders = schannel.dll`.

## Boot chain

### CD / DVD

1. The ISO is made **El Torito bootable** by `mkisofs`, using a boot image /
   loader taken from the Windows source. To boot from CD, Windows requires the
   on-disc filenames to be **uppercase** — hence the `attribute=8` "uppercase"
   flag available in `[SourceDisksFiles]`.
2. Firmware boots the El Torito image, which loads **`setupldr.bin`** (the NT
   setup loader, copied from the source — no binary patching of it was observed;
   PE mode comes entirely from the `/minint` `txtsetup.sif` edit).
3. `setupldr.bin` reads **`txtsetup.sif`** (with the `/minint` os-load options),
   invokes **`ntdetect.com`** for hardware detection, then loads the kernel
   (`ntoskrnl.exe`) and `hal.dll` and the boot-class drivers.
4. The kernel initializes from the BartPE-built registry hives and runs services.
   Because the base registry marks setup as in progress (`Setup\SetupType`),
   **`winlogon.exe` runs the command in `Setup\CmdLine`** rather than presenting an
   interactive logon — and that command launches the configured shell
   (Nu2Menu / nu2shell for a bare build, or Explorer via the XPE plugin).
5. The optional **Boot Fix** plugin adds `bootfix.bin` to enable the familiar
   "Press any key to boot from CD..." prompt.

### Kernel & HAL selection

Because BartPE must boot on unknown hardware, `pebuilder.inf` does not pin a single
kernel/HAL pair:

- **Kernel** — the **multiprocessor** kernel is copied *as* `ntoskrnl.exe`
  (`ntoskrnl.exe=2,ntkrnlmp.exe` in `[SourceDisksFiles]`). The MP kernel also runs
  correctly on uniprocessor machines, so one kernel covers both cases and only the
  HAL has to be chosen at boot.
- **HAL** — several HAL variants are shipped (ACPI-uniprocessor, ACPI-PIC, MPS, …)
  and **no static `hal.dll`**; `setupldr` / `ntdetect.com` detect ACPI/APIC/MP at
  boot and load the matching variant as `hal.dll`, driven by `txtsetup.sif`'s
  `[Hal.Load]` list (see *File selection* in [`07-engine-internals.md`](07-engine-internals.md)).

### USB flash disk (`pe2usb`)

Booting from USB uses a **RAM-disk (SDI)** method and requires loader/ramdisk
files that were **not** part of the XP source, taken instead from **Windows
Server 2003 SP1**:

1. Extract Server 2003 SP1 (e.g. `srsp1.exe -x`) and place into a `srsp1\`
   folder:
   - `setupldr.bin` (the SP1 setup loader), and
   - `ramdisk.sy_` expanded to `ramdisk.sys` (the RAM-disk driver).
2. Build BartPE normally into the `BartPE` output folder (no ISO needed).
3. Run `pe2usb -f <drive:>` to **format** the flash disk and install the files
   (the `-f` format step is only needed the first time; later updates run
   `pe2usb <drive:>` without `-f`).
4. The target PC must be capable of booting from USB and have ≥256 MB RAM.

## Drivers

PE Builder auto-includes drivers placed under the `drivers\` tree. **Only storage
and network drivers are supported.**

| Driver type | Location |
|-------------|----------|
| Storage (mass-storage / SCSI / RAID) | `drivers\SCSIAdapter\<yourfolder>\` |
| Network | `drivers\Net\<yourfolder>\` |

- **Network drivers**: create a subfolder under `drivers\Net` and extract the
  driver files (`.inf`, `.sys`, `.cat`) into it. PE Builder includes them
  automatically.
- **Storage drivers**: these are integrated using the driver's **`txtsetup.oem`**
  file. Because one `txtsetup.oem` can list many variants, you edit its `[SCSI]`
  section and comment out (with `#`) the entries that don't apply (wrong OS such
  as NT 4.0 / 2000, or wrong CPU architecture such as IA-64 / AMD64), leaving
  only the XP/2003 IA-32 entry active.

## Slipstreaming a service pack

A built-in *Slipstream* dialog folds a Microsoft service pack into the source
files:

- Point it at the source **without** the `i386` directory.
- If the source is on read-only media, enable *source is read-only* so files are
  copied to the output location first and slipstreamed there.
- While the service-pack updater runs, `pebuilder.exe` is unresponsive until it
  finishes.

## GUI options (Options dialog)

| Option | Effect |
|--------|--------|
| **Language** | UI language (changing it restarts pebuilder). |
| **Volume ID** | ISO volume label; default `BartPE` if blank. |
| **Verifying data** (StarBurn) | Verify written data after burning. |
| **Don't build registry** | Skip registry-hive building. |
| **Don't build files** | Skip file copying. |
| **Absolute output path** | Treat the output path as absolute rather than relative to pebuilder. |
| **Verbose mode** | Extra (English-only) diagnostic output for troubleshooting. |

## Command-line options

`pebuilder.exe` accepts:

| Argument | Effect |
|----------|--------|
| `-auto` | Automatically start building. |
| `-exit` | Exit after building (does **not** exit if there were build errors). |
| `-overwrite` | Overwrite the target folder without confirmation. |
| `-verbose` | Verbose mode. |
| `-skipregistry` | Skip building the registry files. |
| `-skipfiles` | Skip building the files (handy when testing registry-only changes). |
| `-buildiso` | Rebuild the ISO from the existing output path (also burns if configured). |

The exit code (errorlevel) is the number of errors during building. Example
automation:

```bat
pebuilder.exe -auto -overwrite -exit
if errorlevel 1 echo *** Something was wrong ***
```