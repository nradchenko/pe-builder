<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# terminal-services

Brings up the **WinStation/session subsystem**, so the PE has a console session at all. Off by
default; enabling it auto-pulls **pesuite** (the suite bit, without which none of this runs).

A PE boots without a session: nothing session-gated works, so the theme server (visual styles),
fast user switching, and terminal services have nothing to attach to. This plugin defines and
starts the terminal-services stack and gives it the configuration its init demands.

**This is not about remote desktop.** No listener is configured and remote connections are denied
(`fDenyTSConnections=1`). It is about having a session.

## Pieces

| File | What it is |
|---|---|
| `terminal-services.inf` | the whole plugin: the stack's services, drivers, and configuration |

`termsrv.dll` and its import closure come from the `[Closure]` seed; `termdd.sys` is staged by name
(it ships inside `SP3.CAB` rather than loose).

## The chain, and why both halves are needed

Two things are missing from a PE, and this plugin only supplies the second:

1. **The suite bit** — see the **pesuite** plugin. Without it `winsrv` skips its WinStation init
   entirely.
2. **The session port.** With the bit set, winsrv's init runs and then **blocks**: it creates
   `Global\TermSrvReadyEvent`, waits on it, and connects to `\SmSsWinStationApiPort` only once that
   event is signalled. **termsrv owns that port** — it creates it (building the name at run time,
   which is why a string search finds only the two *connecting* modules) and signals the event on
   its success path. So without this stack, winsrv waits forever and no session ever appears.

The causality here was originally understood **backwards**, twice. The missing port is an *effect*,
not a cause: "termsrv fails because the port doesn't exist" is wrong, and smss is not involved.

## What starts

| Service | Role |
|---|---|
| `termdd` (`Start=1`) | the terminal device driver; system-start, `PortDriverEnable=1`, no Group — as a real XP has it |
| `TermService` (`Start=2`) | Terminal Services: `termsrv.dll` hosted by svchost, depends on `RPCSS`, whitelisted in `Setup\AllowStart` (the setup-mode SCM only auto-starts services listed there) |

## The `svchost` ImagePath trap

`TermService`'s `ImagePath` must read `svchost`, **not** `svchost.exe`:

```
%SystemRoot%\System32\svchost -k DComLaunch
```

The SCM decides which services share a host process by comparing `ImagePath` strings
(case-insensitively), so this has to match DcomLaunch's own value apart from case — as it does on a
real XP, which is where the value was taken from. With `.exe` the strings differ substantively and
the SCM starts TermService in a **separate** svchost that also tries to host DcomLaunch. Measured:
`termsrv.dll` was absent from the DcomLaunch process.

`[Software.AddReg]` adds `TermService` to the DcomLaunch svchost group for the same reason.

## Why the config is close to the source's

The `Terminal Server` tree here — the top-level values, the WinStations security descriptors, the
Console WinStation, and `DefaultUserConfiguration` — is reproduced from an installed Windows XP SP3
(**values only**; reproduce config, never copy code). It is deliberately close to the original
because **termsrv's init fails as a whole** if what it reads does not look like a real
installation, and a smaller sufficient set has never been mapped out.

`Licensing Core` is the sharp example: it is the first key termsrv's init touches, and it is fatal.
termsrv creates it with `KEY_ALL_ACCESS` and, if that fails, returns `STATUS_UNSUCCESSFUL` all the
way out of `ServiceMain` — so it never creates the session port, never signals the event, and
winsrv waits forever. A real XP has the key, empty; pre-creating it means the call opens rather
than creates.

**Trimming this is worthwhile but must be done a value at a time against a real boot, not by eye.**
Everything here was arrived at by measurement, and the failure mode is silent: the desktop still
comes up, just with no session and therefore no theme.

## Connected is enough

The Console WinStation comes up **Connected** (`state=1`), not Active, and **that is sufficient** —
nothing built on this needs a session to reach Active. Expect `state=1`; it is not a bug and not
worth hunting.

Active would require announcing a logon, and nothing here does. It is unnecessary because the theme
client does its own connecting: `shsvcs Ordinal_2` connects the process and allocates the session
context itself — see explorer-shell's README § Visual style. A Connected console session is all
this plugin has to provide.

## What is deliberately absent

- **`wdcon.dll`** — the Console WinStation's `WdDll` names it, but it is a post-SP update file and
  is **not on the install media**. It is not staged, and that works: the Console WinStation still
  comes up Connected without it (measured). The value is kept as the source has it. A long-standing
  theory blamed wdcon for the session never appearing; that theory is **dead** — termsrv has no
  `WdDll`/`wdcon` strings at all.
- **A logon announcement.** `winsta!_WinStationNotifyLogon` was once thought to be required, to
  take the Console WinStation from Connected to Active. It is not: `Ordinal_2` does the connecting
  and the session-context allocation itself (§ Connected is enough), so with the notify compiled
  out entirely **the theme still applies** — that is the measured fact. That it also leaves the
  WinStation Connected rather than Active is an inference, though a safe one: the notify was the
  only thing that ever drove that transition and nothing else announces a logon; the state itself
  has not been re-dumped since the call was removed.

  Removing it was a clear win — it was the only undocumented internal on the boot path: seven args,
  including a block whose layout was never understood and which we fed a zeroed 4KB buffer.
  **Do not add it back without new evidence.**

## What a closure cannot see

`termsrv`'s licensing init calls the **RSA cryptographic provider**, which the SOFTWARE hive names
by *file name* (`Image Path = rsaenh.dll`) — so nothing points at it that an import scan could
follow, and without it `CryptAcquireContext` fails, the licensing init returns
`STATUS_UNSUCCESSFUL`, and `ServiceMain` bails. That file is staged by the **base layer** (which is
where the registration comes from); see `internal/base/base.inf`. This plugin depends on it being there.