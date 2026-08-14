<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# boot-prompt

Makes the CD ask **"Press any key to boot from CD"** and wait, on a machine that has something
else to boot.

```hcl
plugin "boot-prompt" { enabled = true }
```

Disabled by default, so an image boots unattended. Enable it for a disc you carry to real
machines: without the prompt, a machine that boots from its optical drive first and has the disc
in it comes up in the PE rather than in its installed system. Letting the key go unpressed is
what sends the firmware on to the next boot device.

## It does not always ask

The prompt is conditional, and the condition is not "the plugin is enabled". `bootfix.bin` first
reads the MBR of the **first hard disk** (INT 13h, drive `0x80`) and walks its partition table.
It prints nothing and boots straight on if the read fails or if no entry is marked active — so on
a machine with no hard disk, an unreadable one, or a blank unpartitioned one, an image with this
plugin enabled behaves exactly like one without it.

That covers the usual VM: `pebuild run` attaches no disk, so a QEMU boot goes straight in whether
or not you enabled this. To see the prompt there, give the guest a disk with a partition table
that has an active partition — an empty raw image is not enough.

The logic is the file's own, not ours; the above is read out of the blob the Windows source
ships.

## What it stages

One file from the Windows source: `bootfix.bin`, a standalone real-mode blob that holds the
prompt string, the disk check and the wait. Nothing loads it from within the PE — the CD boot
sector looks it up by name at the start of the boot chain, before the setup loader, and skips it
when it is absent.

It is staged as optional, so a source that does not carry the file still builds; the image simply
boots without asking, and nothing reports that. If you enabled this plugin, have a partitioned
disk attached, and still get no prompt, check that the source has the file at all:
`pebuild source ls <source> bootfix.bin`.
