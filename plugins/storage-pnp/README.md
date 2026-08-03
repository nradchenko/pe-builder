<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# storage-pnp

Makes data disks and volumes enumerate and receive drive letters in the `/minint` PE. Off by
default; auto-pulled by anything that needs a writable lettered volume (`explorer-shell` for the
user profile, `ramdrive` for the volume its driver presents).

The plugin **ships no code and needs no toolchain**: it is registry configuration only, written into
the SYSTEM hive.

## Why it exists

A PE boots from the text-setup hive, `setupreg.hiv`, which is skeletal by design — it carries just
enough to run text-mode setup. Its `Enum\Root` is **empty**. The boot disk itself still works,
because `txtsetup.sif` loads the boot storage drivers directly and the boot volume is mounted by the
loader — but nothing enumerates the PnP root, so the partition/volume stack never comes up for any
*other* disk:

```
disk.sys  →  partmgr  →  ftdisk  →  volume  →  mountmgr (drive letter)
```

`disk.sys` and `fastfat.sys` already load via `txtsetup.sif`. The three pieces above them do not
start, so an attached data disk is enumerated as hardware and then goes nowhere: no volume device,
no mount point, no letter. This plugin rebuilds the minimum of that tree.

## What it writes

All of it lands in `[SetupReg.AddReg]` — the SYSTEM hive — under `ControlSet001`:

| Service | What is written | Why |
|---|---|---|
| `PartMgr` | full service config + a `Root\LEGACY_PARTMGR\0000` devnode | The partition manager; turns partitions into volume devices. |
| `VolSnap` | full service config + a `Root\LEGACY_VOLSNAP\0000` devnode | The volume snapshot filter, which sits in the same stack. |
| `ftdisk` | `Group` and `Tag` only | Already boot-loaded by `txtsetup.sif`; only its load-order position is restated. |
| `mountmgr` | `Group` and `Tag` only | Same — it is the one that hands out the letters. |

## Why the nodes are `LEGACY_*` root devices

`partmgr` and `volsnap` are filter drivers on the disk and volume classes: the source's
`hivesys.inf` registers them as `UpperFilters` on the disk (`{4D36E967-…}`) and volume
(`{71A27CDD-…}`) classes, and in a normally installed Windows they are pulled in when a device of
that class starts. That path needs a class installer and a device-install pass — neither of which a
PE runs.

The alternative is what a pre-PnP driver gets: a **root-enumerated legacy-driver node**. Each is a
`ControlSet001\Enum\Root\LEGACY_<NAME>\0000` key of class `LegacyDriver`
(`{8ECC055D-047F-11D1-A537-0000F8753ED1}`), bound to its service by the `Service` value, with the
matching `Services\<name>\Enum` back-reference (`0`, `Count`, `NextInstance`) that the PnP manager
expects on a service with a devnode. This is the same shape `ramdrive` uses for its own root device,
and it is registration only — no install pass has to run for the node to exist.

They are **boot-start** (`Start=0`, `Type=1`), so the loader loads the images regardless; the node is
what gives the PnP manager something to attach them to.

`ConfigFlags` is `0x20` (`CONFIGFLAG_FINISH_INSTALL`) here. Note the contrast with `ramdrive`, whose
node must use `ConfigFlags=0` — there the flag would hold a **demand-start** (`Start=3`) driver
pending a usermode finish-install pass that never runs in a PE, and the service would read
`NEVER_STARTED`. A boot-start driver is already loaded by the time any of that would matter, which
is why the same flag is harmless here. See [`ramdrive/README.md` § Registration](../ramdrive/README.md#registration).

## Values that came from the source, and one that did not

The service configuration is not invented: `Start`, `Type`, `ErrorControl` and `Group` for all four
services match the source's own `hivesys.inf`. Checked with:

```sh
pebuild source extract <src> hivesys.inf -o - | grep -iE 'Services\\(PartMgr|VolSnap|Ftdisk|MountMgr)"'
```

Load-order **tags** within `System Bus Extender` are ordered by the group's `GroupOrderList` value,
which lists tags in load order. On XP SP3 media that list is `03, 04, 01, 08, 09, 0a, 0b, 0c, 0d,
0e` (`hivesys.inf`), and `…, 0f` in `setupreg.hiv`. So:

- `mountmgr` `Tag=8` and `ftdisk` `Tag=9` — **match the source exactly**, and sit fourth and fifth
  in the group.
- `VolSnap` gets **no** `Tag` — matching the source, which assigns it none.
- `PartMgr` is written `Tag=5`, but the source assigns it **`Tag=0xa`**, and **tag 5 appears in
  neither `GroupOrderList`**. A tag that is not in the list has no assigned slot, so PartMgr does not
  load where the source would load it (sixth, directly after ftdisk).

  Where `5` came from is not recorded, and no reason for departing from the source is known. It has
  not caused an observed problem — disks enumerate and get letters — most likely because PartMgr does
  its real work as a disk-class upper filter attached when the disk devnode starts, rather than from
  its slot in the group order. **Treat it as unexplained rather than deliberate:** it is on the boot
  path, so changing it to `0xa` is a boot-tested change, not a tidy-up.

## The `Driver` values reference nothing

Each node carries a `Driver` value — `{8ECC055D-…}\0020` for PartMgr, `{8ECC055D-…}\0000` for
VolSnap — naming a driver key under `Control\Class\{LegacyDriver}\<instance>`. **Those keys are
never created**, by this plugin, the base layer, `hivesys.inf`, or `setupreg.hiv` (verified: the
class key is absent from the source's text-setup hive entirely). The instance numbers are therefore
arbitrary and the references dangle.

It works because the binding that matters is `Service` (and `Control\ActiveService`), which is what
the PnP manager follows to load the driver; the `Driver` value is the setupapi/Device-Manager path to
a driver's own configuration. Anything that walks it — a properties dialog, an install pass — finds
nothing there.

## What it does not do

- **It does not format or partition anything.** It makes existing volumes visible and lettered.
- **It does not stage any files.** `partmgr.sys`, `volsnap.sys`, `ftdisk.sys` and `mountmgr.sys` all
  come from the boot driver set the base layer already derives from `txtsetup.sif`.
- **It does not pin drive letters.** The Mount Manager assigns them in enumeration order. A plugin
  that needs a fixed letter asks for one itself — `ramdrive`'s `DriveLetter` value is the example.
