<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# explorer-shell

Boots the PE to a classic Windows Explorer desktop — Start menu, taskbar, and Control
Panel — instead of a bare `cmd.exe`. Off by default; enabling it auto-pulls its
dependencies **win32-gui** (the shared Win32 GUI runtime) and **storage-pnp** (so a
writable lettered volume exists for the user profile).

A `/minint` PE has no Winlogon or user session, so the desktop can't just be switched on
with a registry value — the shell has to be brought up by hand. This plugin does that. The
bring-up follows the behavior of the *XPE* plugin for Bart's PE Builder (by Sherpya),
documented under [`docs/reference/`](../../docs/reference); the mechanisms below are
reimplemented independently here.

## Pieces

| File | Produces | Role |
|------|----------|------|
| `explorer-shell.inf` | — | Manifest: dependency/closure/build declarations and the registry, file and text edits. |
| `peshell.c` | `peshell.exe` | The `SYSTEM\Setup\CmdLine` launcher — brings the shell up. |
| `peshut.c` | `peshut.dll` | The shutdown power-off hook (see [Shutdown](#shutdown)). |

## Boot sequence (`peshell.exe`)

`peshell` runs as the setup command line and performs, in order:

1. **Profile** — find a writable fixed/removable volume, create the user-profile skeleton
   on it, and point `USERPROFILE` / `ALLUSERSPROFILE` at it.
2. **Splash** — show the "Please wait…" status window (see [Splash](#splash)).
3. **Wait for services** — block until the SCM/COM baseline is reachable. It does *not*
   wait for every service to reach RUNNING; they finish in the background while the desktop
   comes up.
4. **Self-registration** — run the shell/COM `DllInstall` pass (see
   [Self-registration](#self-registration)); without it Explorer exits immediately.
5. **Start menu** — create the shortcuts declared in the registry (see
   [Start-menu items](#start-menu-items)).
6. **Prune ghost Control Panel items** — drop `ControlPanel\NameSpace` entries whose
   backing handler DLL isn't present/loadable, so they don't render as empty folders.
7. **Launch Explorer** — start `explorer.exe` as the shell, close the splash once the
   taskbar paints, relaunch Explorer if it dies, and finally fall back to `cmd.exe` so the
   machine is never headless.

## Self-registration

Explorer's shell/COM classes aren't registered in a bare `/minint` hive, so it would fail
to initialize. `peshell` runs the shell and IE COM DLLs' own `DllInstall` self-registration
(under `OleInitialize`, after the COM baseline is up), which is what lets Explorer's
namespace come up. `ie4uinit.exe` in that pass reads `ieuinit.inf` (staged via
`[SourceDisksFiles]`).

## Splash

The "Please wait…" window mimics the Windows startup/shutdown status dialog: a title bar,
the Windows branding banner (loaded from the source's `msgina.dll`) across the top, a thin
running blue↔orange bar beneath it, and the current phase text. It runs on its own thread
so it keeps animating during the bring-up.

## Shutdown

`/minint` has no Winlogon, so the shell's "Turn Off Computer" call to `ExitWindowsEx`
routes to a shutdown coordinator that isn't there and fails silently — nothing powers off.
`peshut.dll` fixes this: it is loaded into shell processes via `AppInit_DLLs` and, on load,
inline-patches `user32!ExitWindowsEx` so any shutdown/restart request instead drives the
kernel's ACPI path directly (`NtShutdownSystem`) — the same primitive as the `poweroff`
plugin. Restart maps to reboot, everything else to power-off. `Log Off` is removed from the
shutdown dialog (there is only the SYSTEM user) via the `NoLogoff` policy.

## Visual effects

The desktop uses the classic no-frills look (no animations/fades/shadows). These are
win32k *session* settings: the baked `Control Panel\Desktop\UserPreferencesMask` and
`WindowMetrics\MinAnimate` are **not** enough on their own — during shell init XP runs an
"apply recommended visual effects" pass that recomputes the mask and overwrites the live
`HKU\.DEFAULT` copy. The `Explorer\VisualEffects\<effect>\DefaultApplied=1` flags (plus
`VisualFXSetting=3`) tell XP those defaults are already applied, suppressing the recompute
so the baked values survive. Both halves are required.

## Start-menu items

Top-level Start-menu shortcuts are data-driven. Each value under
`HKLM\SOFTWARE\pe-builder\StartMenu` is one item — the value name is the label, the data is
the (env-expandable) target path. `peshell` reads them at boot and writes a `.lnk` into the
Start Menu root for each. Add or remove items by editing the `[Software.AddReg]` lines; no
code change is needed.

A side effect that is also relied on: the classic Start menu only draws its vertical side
banner when the menu is tall enough. `NoFind`/`NoSMHelp` remove two rows; keeping a couple
of items here restores the height so the banner reappears.

## Display resolution

The desktop mode is set on the active video device instance,
`ControlSet001\Control\Video\{GUID}\0000\DefaultSettings.*` (where win32k reads it via
`HARDWARE\DEVICEMAP\VIDEO`), not only under `Services\VgaSave\Device0`. Whether a given
mode actually comes up still depends on the display driver's capabilities.