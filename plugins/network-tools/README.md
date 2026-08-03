<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# network-tools

The command-line network utilities, for a PE that has a network: `ipconfig`, `ping`, `route`, `arp`,
`tracert`, `nslookup` and `hostname`.

Off by default. Enabling it auto-pulls **`hardware`**, which is what actually configures the network
at boot — these are only the tools for looking at it.

## Why it is a separate plugin

Configuring devices and handing a user tools are different concerns. Keeping them apart means a build
that wants the network up need not carry the utilities, and the dependency runs one way only: enable
`network-tools` and `hardware` comes with it, enable `hardware` alone and it does not.

The tools are unusually cheap to add — by the time there is a network to inspect, `hardware` has
already staged the stack they link against, so each one costs little more than its own binary.

## The file set is computed

Every tool is a `[Closure]` seed: `pebuild` walks its static import closure against the Windows
source and stages whatever it finds. Nothing is hand-listed, so a tool that grows a dependency does
not silently lose it.

## The two hand-seeded DLLs

`mgmtapi.dll` and `inetmib1.dll` are named in the closure list explicitly, which is otherwise
something this plugin never does.

`arp` does not read the ARP table through a private API — it goes through **SNMP's MIB-II path**, and
loads that provider **by name at run time**. A static import closure cannot see a `LoadLibrary` call,
so neither DLL appears in `arp.exe`'s closure at any depth (checked with
`pewalk --iso <src> arp.exe`). Without them `arp` starts and then fails with an error that names
nothing, which is the worst kind of missing file: the tool looks present and broken rather than
absent.

## What is deliberately left out

**`netstat`** is the notable omission. It loads `xpsp2res.dll` for its message strings, and that file
is on **no form of this media** — not loose, not compressed, not in a cabinet (`pebuild source ls
<src> xpsp2res.dll` matches nothing). Being a runtime resource load, it is invisible to the closure
too, so the tool would stage cleanly and fail at first use.

The rule this follows: **a tool that cannot work in this image is not staged.** One that always fails
reads as a broken network, and sends whoever hits it looking for a configuration fault that is not
there; a tool that simply is not present reads as a missing feature, which is the truth.

Anything else absent from the seed list is absent for the same reason — check what it loads before
adding it, and prefer leaving it out over shipping it broken.
