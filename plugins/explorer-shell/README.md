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
| `startmenu.c` / `.h` | (into `peshell.exe`) | Pure Start-menu layout planner (see [Start-menu items](#start-menu-items)); host-tested by `smtest/`. |
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
7. **Visual style** — apply the style the registry selects, if any (see
   [Visual style](#visual-style)). Before Explorer, so the desktop paints themed from its
   first frame; a no-op when no visual-style plugin is enabled.
8. **Launch Explorer** — start `explorer.exe` as the shell, close the splash once the
   taskbar paints, relaunch Explorer if it dies, and finally fall back to `cmd.exe` so the
   machine is never headless.

## Self-registration

Explorer's shell/COM classes aren't registered in a bare `/minint` hive, so it would fail
to initialize. `peshell` runs the shell and IE COM DLLs' own `DllInstall` self-registration
(under `OleInitialize`, after the COM baseline is up), which is what lets Explorer's
namespace come up. `ie4uinit.exe` in that pass reads `ieuinit.inf` (staged via
`[SourceDisksFiles]`).

## Visual style

`peshell` applies the visual style the registry selects, before it launches Explorer — the same
place and the same handshake Windows uses, where winlogon loads `msgina.dll`, which delay-loads
`SHSVCS.dll` (the Themes service's *client* half) and drives the apply before starting the shell.
peshell stands in for winlogon.

**The contract** is four values under `HKCU\Software\Microsoft\Windows\CurrentVersion\ThemeManager`
— `ThemeActive`, `DllName`, `ColorName`, `SizeName` — written by whichever visual-style plugin is
enabled (see `luna`). Nothing here is theme-specific: peshell reads what is selected and applies
it, so a visual style ships no code. With no style selected it returns at the first check, and
`shsvcs.dll` isn't even staged — the PE keeps the classic look. `DllName` is a `REG_EXPAND_SZ` and
peshell expands it: **this PE boots from CD and its system drive is not `C:`**, so nothing may
rebuild that path by hand.

**Why peshell owns this, rather than the Themes service** (read out of the binaries, not assumed):

- The service *cannot* apply a style itself. `shsvcs` delay-imports uxtheme ordinals 20-33, 63 and
  66 — but **not #34**, the `RegisterUserApiHook` path. The theme hook is always installed by the
  *client*, in the client's own process.
- The hook lives exactly as long as that client's `\ThemeApiPort` connection: `shsvcs` imports
  `UnregisterUserApiHook` and drops the hook when the client goes away. The applier must therefore
  outlive the desktop — peshell does. A helper that applied the style and exited had it torn down
  about a second later.

**The apply is two calls.** `shsvcs Ordinal_2(dwTimeoutMs)` connects this process as a theme client
— it waits for the Themes service itself (opening the SCM and polling), allocates the session
context, and turns the hooks on, so `IsThemeActive` is already true when it returns. Then
`uxtheme #65 SetSystemVisualStyle` selects the style; it only *checks* that the hooks are on and
fails with `0xd0000001` if they are not — it cannot install them. `Ordinal_2` is the same export
`msgina` drives at logon, which is why msgina imports shsvcs ordinals 1-6 rather than just the
apply.

**The timeouts are short on purpose.** Both this and the wait for the session subsystem
(`Global\TermSrvReadyEvent`, which winsrv signals only once termsrv creates the WinStation port)
sit on the boot path, before Explorer: every second waited is a second of no desktop, and a timeout
costs only the theme. Both have measured 0ms on every boot.

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

Start-menu shortcuts are data-driven. Each value under `HKLM\SOFTWARE\pe-builder\StartMenu`
is one item, and `peshell` reads them at boot and materializes a `.lnk` for each. Any plugin
can contribute items by writing these values from its own `[Software.AddReg]`; no code change
is needed. The layout logic lives in the pure `startmenu.c` planner (host-tested by
`smtest/`), separate from the Windows side that creates the folders and `.lnk` files.

**The contract** — for one value:

- **Value name = the menu path.** A backslash encodes a submenu: `Games\Solitaire` places
  *Solitaire* in a *Games* submenu; `Games\Board\Chess` nests two deep. A bare name (no
  backslash) sits at the top of *All Programs*. A **single leading backslash** anchors the
  entry at the **Start Menu root** instead — the classic menu's top level, above *Programs*:
  `\Command Prompt` is a root item, `\Games\Chess` a submenu off the root. Intermediate folders
  are created as needed.
- **Value data = the target**, `%var%`-expandable, optionally `target|Explicit Label`.
- **The displayed label** is resolved by precedence:
  1. the **explicit label** after `|` in the data, if given (an override);
  2. otherwise the target's version-resource **`FileDescription`** — which XP ships
     **localized** for the source's language, so the menu reads in that language for free
     (ties to the source-locale work); no hardcoded label table;
  3. otherwise the **leaf** of the value name.

  The chain never yields an empty label, so a target with no version resource still gets a
  sensible name from its value-name leaf.

Items are placed under **`Start Menu\Programs`** (the *All Programs* tree), the standard XP
structure — except a leading-backslash entry, which is placed in the **Start Menu root** (the
classic menu's top level). Backslash characters that are path separators in the name become
real folders; any character that is illegal in a filename is stripped from the on-disk
`.lnk`/folder name.

## Display resolution

The desktop mode is set on the active video device instance,
`ControlSet001\Control\Video\{GUID}\0000\DefaultSettings.*` (where win32k reads it via
`HARDWARE\DEVICEMAP\VIDEO`), not only under `Services\VgaSave\Device0`. Whether a given
mode actually comes up still depends on the display driver's capabilities.

The mode is **configurable from the manifest**. The plugin declares `width`, `height`, and
`bpp` options (`[Options]`, defaults 1024×768×32) and templates them into the
`DefaultSettings.*` values with `$(name)` placeholders. A build overrides them in the
plugin block:

```hcl
plugin "explorer-shell" {
  enabled = true
  options {
    width  = 1280
    height = 1024
    bpp    = 16
  }
}
```

`bpp` is constrained to the
colour depths the boot VGA driver plausibly accepts (`8`/`16`/`24`/`32`); `width`/`height`
are unconstrained, so a mode the boot VGA driver does not support will simply not come up —
stick to the common 4:3/widescreen modes it honours.