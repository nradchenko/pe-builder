# wim-cd-boot

The wiring meta-plugin for the distributable WIM boot layout (`layout = "wim"`, which enables
it implicitly). It declares the boot-start registry contract and `txtsetup.sif` edits the WIM
overlay needs, and depends on the out-of-tree plugins.

For how the whole chain works and how to build an image, see
[`docs/wim-boot.md`](../../docs/wim-boot.md). This README covers only *this plugin's* wiring
decisions.

## What it wires

The overlay chain needs two drivers running boot-start, before the session manager: the FltMgr
filter manager and the `wimmf` overlay filter that reads the file tree out of the WIM. This
plugin creates their service keys, the devnode FltMgr needs, and the `txtsetup.sif` entries that
get them into the boot set. `firadisk` and `grub4dos` are pulled in as dependencies but wire
themselves.

## FltMgr

`fltmgr.sys` comes from the Windows **source**. The service key is a boot-start (`Start=0`, `Type=2`)
filter in the **`FSFilter Infrastructure`** group.

FltMgr has no PnP INF that a PE runs, so nothing would create its device. We build the
`Root\LEGACY_FLTMGR` legacy devnode by hand — the `Enum` back-reference on the service, the
`Enum\Root\LEGACY_FLTMGR\0000` node, its `LegacyDriver` class, and the `Control\ActiveService`
link. The whole block was **measured from a reference WinPE**.

## wimmf

`wimmf.sys` is staged by its **own out-of-tree plugin**; this plugin only creates the service
key. It is boot-start (`Start=0`, `Type=2`), depends on `fltmgr`, and sits in the
**`FSFilter Compression`** group at **altitude 161000** — the band the filter manager uses to
order compression filters, which is where an image-backing overlay belongs.

## WimPath

`WimPath` tells `wimmf` where the WIM is: `\Device\CdRom0\PE.WIM`. Two things about that value:

- **Case.** The wim-cd tail writes the file to the media root verbatim as `PE.wim`
  (`build/wimcd.go`, `wimCDImageName`) — the outer ISO is mastered without the uppercasing the
  loose-file `iso` target does. The uppercase `PE.WIM` here still resolves it because cdfs and
  `wimmf` match the path case-insensitively.
- **Drive number.** `CdRom0` is a hint, not a hard requirement. `wimmf` resolves the actual CD
  device by scanning `CdRom0..9` for this path **suffix** (its stable-name fallback), so the WIM
  is found whatever number the CD drive enumerates as.

## Boot-load order

The `[SetValue]` edits add `fltmgr` then `wimmf` to `txtsetup.sif`'s `BusExtenders.Load`, so
`setupldr` pulls both into the boot set and the kernel starts them boot-start, ahead of the
session manager. `firadisk` boot-loads itself through its own plugin's `SCSI.Load` edit, so it
is not listed here.

## Dependencies

`depends = wimmf, firadisk, grub4dos` — the three out-of-tree plugins that carry the binaries
this contract points at:

- **`wimmf`** — the WIM overlay filter driver (the `wimmf.sys` this plugin's service key names).
- **`firadisk`** — reclaims the grub4dos-mapped RAM region as a disk; self-wiring.
- **`grub4dos`** — the loader that maps the boot core into RAM as a CD.

If any is missing at build time, the build stops and names it.