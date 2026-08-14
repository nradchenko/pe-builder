<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# ramdrive

A writable scratch volume backed by RAM, so a standalone boot needs **no attached disk**
for the user profile and temp files. Off by default; enabling it auto-pulls **storage-pnp**
(which brings up the partition/volume/Mount Manager stack the driver plugs into).

The plugin ships a small kernel driver, `ramdrive.sys`, compiled from source at build time
by the `[Build]` pre-pass. At boot the driver allocates a fixed-size buffer, self-formats it
as an empty FAT16 volume, and registers the Mount Manager device interface; the Mount
Manager then assigns the volume a drive letter and the kernel hands it to FastFAT on first
access.

## Pieces

| File | Produces | Role |
|------|----------|------|
| `ramdrive.inf` | — | Manifest: dependency/build declarations and the PnP devnode + service registration. |
| `ramdrive.c` | `ramdrive.sys` | The WDM PnP function driver. |
| `Makefile` | — | MinGW-w64 cross-build (see [Build](#build)). |

## Architecture

`ramdrive.sys` is a WDM **PnP function driver**, not a legacy driver. The PnP manager
enumerates the `Root\RAMDRIVE` devnode (registered by the INF) and creates the physical
device object (PDO); the driver's `AddDevice` creates the function device object (FDO — a
`FILE_DEVICE_DISK` with `DO_DIRECT_IO`) and attaches it to the stack. An `IO_REMOVE_LOCK`
guards teardown against in-flight I/O.

A PnP function driver is used deliberately: the clean way to get a drive letter the shell
sees is the Mount Manager device interface (`MOUNTDEV_MOUNTED_DEVICE_GUID`), and
`IoRegisterDeviceInterface` requires a PDO. Registering that interface makes the Mount
Manager assign the letter *and* notify the shell — which a self-created `\DosDevices\R:`
symlink cannot do. This is the model every working native-WinPE ramdisk uses.

The PnP flow: on `IRP_MN_START_DEVICE` the driver forwards the IRP down and waits, then
allocates the backing store, formats it, and registers + enables the interface.
`QUERY_CAPABILITIES` is forwarded and then *amended* (non-removable, surprise-removal OK).
`REMOVE` disables the interface, then — after the remove lock drains in-flight I/O — frees
the pool and detaches/deletes the FDO; `SURPRISE_REMOVAL` disables the interface and leaves
the free to the `REMOVE` that follows. Everything else is passed down to the PDO.

## Backing store and size

The disk is a flat `ExAllocatePoolWithTag(NonPagedPool, …)` block — non-paged because I/O
may run at raised IRQL. Non-paged pool is a limited resource on 32-bit XP, especially early
in boot, so the default is a conservative **8 MB** and the size is clamped to **4–16 MB**.
The lower bound keeps the FAT16 cluster count above the FAT12/16 boundary (~4085 clusters)
so FastFAT mounts it as FAT16, not FAT12.

Reaching tens of MB reliably would mean backing the disk with `MmAllocatePagesForMdl`
physical pages instead of a flat pool block; that is out of scope for this version. If
`DiskSize` is raised past what non-paged pool can satisfy, the allocation fails and
`START_DEVICE` fails cleanly (the devnode won't start) rather than crashing.

## FAT16 super-floppy format

The volume is a FAT16 **super-floppy** — a filesystem at sector 0 with no partition table.
The driver self-formats the zeroed buffer: a BIOS Parameter Block, the two reserved FAT
entries in both FAT copies, and a root-directory volume-label entry so the volume shows as
`PE RAMDISK`. Sectors-per-cluster grows for larger disks to keep the cluster count inside
the FAT16 range; it can also be pinned via a parameter. The layout follows the public FAT
specification.

Don't change that label: a WDM disk reports `DRIVE_FIXED` like a real one, so it is all that
tells this volume from an attached disk — `explorer-shell` matches it to place the profile here.

## Disk and Mount Manager IOCTLs

FastFAT and the volume/Mount Manager stack probe the disk through IOCTLs, which the driver
answers in `IRP_MJ_DEVICE_CONTROL`:

- **Disk/storage geometry & identity** — `GET_DRIVE_GEOMETRY(_EX)`, `GET_LENGTH_INFO`,
  `GET_PARTITION_INFO(_EX)` (the whole disk as one *recognized* FAT partition at offset 0),
  `STORAGE_GET_HOTPLUG_INFO` (non-removable), `GET_DEVICE_NUMBER`,
  `VOLUME_GET_VOLUME_DISK_EXTENTS`, plus `IS_WRITABLE`/`CHECK_VERIFY`/`MEDIA_REMOVAL`.
- **Mount Manager** — `IOCTL_MOUNTDEV_QUERY_DEVICE_NAME` (the FDO's `\Device\…` name),
  `QUERY_UNIQUE_ID` (a fixed id, since this driver exposes one volume), and
  `QUERY_SUGGESTED_LINK_NAME` (suggests `\DosDevices\<letter>:` only when a letter is
  pinned; otherwise the Mount Manager auto-assigns).

MinGW omits `ntdddisk.h`/`ntddstor.h`, so the disk IOCTL codes and structs are hand-defined
in the source (stable public ABI); the Mount Manager headers *are* shipped. Any unhandled
IOCTL is logged via `DbgPrint` (a `func 0x…` code) so the answered set can be grown from a
kernel-debug trace rather than guessed.

## Parameters

Read on `START_DEVICE` from `HKLM\SYSTEM\…\Services\ramdrive\Parameters` via `Zw*`
(not `RtlQueryRegistryValues`, whose `RTL_QUERY_REGISTRY_DIRECT` path faults on a missing
`REG_SZ` default):

| Value | Type | Meaning |
|-------|------|---------|
| `DiskSize` | `REG_DWORD` | Backing-store size in bytes (default 8 MB, clamped 4–16 MB). |
| `DriveLetter` | `REG_SZ` | Pins the mount point (e.g. `R:`); omit to let the Mount Manager auto-assign. |
| `SectorsPerCluster` | `REG_DWORD` | Optional cluster-size override; otherwise chosen automatically. |

## Registration

The INF registers a fully-installed root PnP device: a `Root\RAMDRIVE\0000` devnode with a
`HardwareID`, bound via `Driver` + `Control\Class` to a demand-start (`Start=3`) service.
The kernel PnP root enumeration loads `ramdrive.sys` at boot and calls its `AddDevice`.

`ConfigFlags` is **0** — the node is fully installed and needs no further action, so PnP
starts it. It must *not* be `CONFIGFLAG_FINISH_INSTALL` (`0x20`): that marks the install
incomplete and holds the device pending a usermode finish-install pass that never runs in a
PE, so a demand-start driver is never loaded (the service reads `NEVER_STARTED`). The
`PlugPlay` service the enumeration needs is already whitelisted by the base layer.

## Build

`ramdrive.sys` is cross-compiled with a **MinGW-w64** toolchain — no WDK, no MSVC. It is a
freestanding native-subsystem PE with entry point `DriverEntry` that keeps its `.reloc`
section, exports no symbols, and carries a valid PE checksum — all load requirements for an
XP kernel driver. Verified with **GCC 13.2**, **GNU binutils (ld) 2.41**, and **mingw-w64
headers 11.0**.

The driver source is architecture-neutral; only the toolchain differs. `make ARCH=i386`
(the default) and `make ARCH=amd64` select it, and the plugin's `[Build.<arch>]` sections
name each architecture's compiler and
pass the matching `ARCH`. One switch at the top of the `Makefile` carries the whole
difference: toolchain triple, arch define, entry-point decoration (x64 has no `_…@8`),
native subsystem version, and DDK include path. An `ARCH` the recipe has no flags for is an
error rather than a fallback, so a wrong-architecture driver cannot be built silently.

**The amd64 build is not yet boot-proven.** It compiles, and its source carries no
pointer-width assumptions (offsets are `LARGE_INTEGER`, the backing is capped well inside
`ULONG`), but unlike `pesuite` — a leaf that writes one word — this driver has dispatch and
completion paths that can fault, and the recipe suppresses unwind tables
(`-fno-asynchronous-unwind-tables`), which x64 uses for exception dispatch and stack walks.
Treat x64 as untested until it has been booted.

The exact compiler/linker flags, and the couple of MinGW-ddk header workarounds the build
needs, are documented in the `Makefile` and source comments.

`DbgPrint` tracing is opt-in: build with `-DRAMDRIVE_DEBUG` to enable it when debugging with
a kernel debugger attached. It is off by default (and fully compiled out) — a shipped PE has
no debugger to read it, so there is no reason to pay the formatting cost on the I/O path.

## Sources

The driver is written from public documentation, and each technical requirement in
`ramdrive.c` is tagged with the source it derives from:

- `[FATspec]` — the [Microsoft FAT File System Specification][fatspec] (the BPB + on-disk
  layout).
- `[WDM-PnP]` — the WDK [WDM Plug and Play driver model][wdm] (`AddDevice`, PnP IRPs,
  `IO_REMOVE_LOCK`, forward-and-wait, capability amendment).
- `[MountMgr]` — the WDK [Supporting Mount Manager Requests in a Storage Class Driver][mountmgr]
  interface and the `mountmgr.h`/`mountdev.h` IOCTL contract.
- `[ntddk]` — the WDK [`ntddk.h` kernel and `Zw*` registry APIs][ntddk]; the disk IOCTL codes
  and structs are hand-defined here because MinGW omits their headers.

[fatspec]: https://download.microsoft.com/download/1/6/1/161ba512-40e2-4cc9-843a-923143f3456c/fatgen103.doc
[wdm]: https://learn.microsoft.com/en-us/windows-hardware/drivers/kernel/introduction-to-wdm
[mountmgr]: https://learn.microsoft.com/en-us/windows-hardware/drivers/storage/supporting-mount-manager-requests-in-a-storage-class-driver
[ntddk]: https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntddk/