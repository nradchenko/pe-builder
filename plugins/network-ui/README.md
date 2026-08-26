<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->

# network-ui

The Windows network UI on the Explorer desktop: the **Network Connections** folder in Control
Panel, a **notification-area icon**, and the adapter **Status** and **Properties** dialogs behind
it.

Without it a build can have a fully working network — `hardware` configures one, `network-tools`
gives you `ipconfig` and `ping` — and no sign of it anywhere in the UI. This plugin adds the sign,
not the network.

It ships no code and needs no toolchain: everything it displays is Microsoft's own shell
components. Its substance is staging what those components load **by name** — invisible to any
import closure — and registering them. On a themed desktop it adds about 5 MB, two thirds of that
the resource satellites below.

## What it stages, and why a closure walk cannot see it

- **`netshell.dll`, `netman.dll`, `ncpa.cpl`** — the folder and its classes, the connection
  manager, and the Control Panel stub. The shell loads each by name. They are seeded here rather
  than inherited from `hardware`: on x86 media its `netcfgx.dll` imports netshell and netman, on
  amd64 media it imports neither.
- **`hnetcfg`, `rasdlg`, `wzcdlg`** — delay-loaded by netman and netshell, so absent from the
  static import table. A failed delay-load raises at the first *call*, far from the cause.
- **The service-pack resource satellites** — `xpsp1/2/3res` on XP, `ws03res`/`w03a2409`/`xpsp2res`
  on 2003. The dialogs read their templates from these; without them the Properties sheet does not
  open, silently. Scoped by **build number, not architecture** — the split follows the source's
  codebase — and staged **optional**, because a localized source need not carry them at all and a
  required entry would fail such a build outright.
- **`stobject.dll`** — see below.

`ntmarta.dll` is staged by `explorer-shell` instead, being DCOM infrastructure rather than a
network concern: without it netman runs without registering its class object and every activation
fails with `CO_E_SERVER_EXEC_FAILURE`.

## Registration

Both DLLs export `DllRegisterServer` and go through the shell's self-registration contract
(`HKLM\SOFTWARE\pe-builder\SelfRegister` — see `explorer-shell`). netshell's own registrar writes
the folder CLSID, the connection UI classes, the `Shell Extensions\Approved` entries **and its
Control Panel entry**, which is why this plugin declares no namespace key of its own.

`atl.dll` is in that list too, and not for its own sake: both DLLs register through ATL's
registrar, a COM class that nothing in a PE registers. Without it both calls fail before writing a
key. Entries register in sorted name order, so naming it is enough.

`netman` also needs `Setup\AllowStart\Netman`: the setup-mode SCM starts only whitelisted services,
and netman is demand-started by the COM launcher.

## The tray icon

The icon is not drawn by netshell alone. `stobject.dll` — the `SysTray` shell service object —
instantiates netshell's tray class, and carries that class id as a **binary GUID**, so nothing
naming it appears anywhere in the file. Nothing else on the image instantiates it, and without
`stobject.dll` there is no tray icon **and no Status dialog**: both are raised through that object.

`explorer-shell` removes the `SysTray` entry deliberately — useless in a PE with no audio and no
network — so this plugin stages `stobject.dll` and puts the entry back. Deps-first plugin order is
what makes that work.

The icon is **off by default**; the user ticks "Show icon in notification area when connected", as
on stock XP. Setting it at build time is not currently possible — the connection's key is minted at
run time, and the tray only reacts to its own start-up enumeration or a change notification.

## Limitations

- **Localized sources: the network works and the folder and tray work, but the dialogs do not** —
  such a medium carries none of the resource satellites. Everything upstream is healthy there:
  netman activates, the connection enumerates, and the connection's UI class creates, so the
  failure is at the resource layer. (Where a shipping localized Windows keeps those resources is a
  separate question and is not known; they are not on the medium in any form.)
- **The Advanced tab reports that WMI may be corrupted.** A PE stages no `wbem` subsystem.
- **Only what the network pass produced appears** — an empty folder is a faithful report that no
  adapter was bound.

`pehw netconn` reports the connection from both ends — its registry entry and netman's live view —
which is the quickest way to tell which layer is failing.
