<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# explorer-shell

Boots the PE to a Windows Explorer desktop — the modern XP Start menu, taskbar, and Control
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
4. **Startup tasks (`pre-shell`)** — walk the run list, if the `startup` plugin staged its
   runner (see [Startup tasks](#startup-tasks)). After the service wait, because a device or
   network install pass needs the service stack up.
5. **Self-registration** — run the shell/COM `DllInstall` pass (see
   [Self-registration](#self-registration)); without it Explorer exits immediately.
6. **Start menu** — create the shortcuts declared in the registry (see
   [Start-menu items](#start-menu-items)), and put the account picture where the Start panel
   looks for it (see [Account picture](#account-picture)).
7. **Prune ghost Control Panel items** — drop `ControlPanel\NameSpace` entries whose
   backing handler DLL isn't present/loadable, so they don't render as empty folders.
8. **Visual style** — apply the style the registry selects, if any (see
   [Visual style](#visual-style)). Before Explorer, so the desktop paints themed from its
   first frame; a no-op when no visual-style plugin is enabled.
9. **Launch Explorer** — start `explorer.exe` as the shell; once the taskbar paints, run the
   `post-shell` startup tasks, then close the splash. Relaunch Explorer if it dies, and
   finally fall back to `cmd.exe` so the machine is never headless.

## Startup tasks

When the `startup` plugin is enabled, `peshell` walks its task list: the `pre-shell` stage
before the shell comes up, `post-shell` once the desktop is there. Each task's label shows on
the splash while it runs, and a task that fails is named there rather than failing silently —
the boot continues either way.

Nothing here requires it. `explorer-shell` does not depend on `startup`, so a build that
declares no tasks carries no runner at all. The contract, and what a task may declare, belong to
that plugin — see its README.

## Self-registration

Explorer's shell/COM classes aren't registered in a bare `/minint` hive, so it would fail
to initialize. `peshell` runs the shell and IE COM DLLs' own `DllInstall` self-registration
(under `OleInitialize`, after the COM baseline is up), which is what lets Explorer's
namespace come up. `ie4uinit.exe` in that pass reads `ieuinit.inf` (staged via
`[SourceDisksFiles]`).

That sequence is validated only as a whole — its order is load-bearing, so it is fixed in
`peshell.c` and nothing outside can reorder it.

A plugin that stages a COM server of its own declares it under
`HKLM\SOFTWARE\pe-builder\SelfRegister`, one value per DLL, the value name being the file (the
data is unused). `peshell` enumerates that key and calls each one's `DllRegisterServer` **after**
the fixed sequence. The point is that no plugin's code names another plugin's files: `wallpaper`
stages an HTML engine and says so itself, and a build without that plugin has no key, so the
pass is a no-op.

Entries are registered in sorted name order. They are meant to be self-contained servers whose
relative order does not matter, but registry value enumeration order is a property of the hive's
value list rather than a guarantee — sorting means that if one entry ever does depend on another,
it fails the same way every time instead of intermittently.

Registration is best-effort — nothing here aborts the boot — but it is not silent. A DLL that
will not load, one missing the entry point, and one returning a failure `HRESULT` are each
reported by name and reason. A component that never registers surfaces far from here, as an
applet that dies or a dialog that wedges, with nothing pointing back at this pass.

## Diagnosing a boot that stops partway

`peshell` reports its progress to the kernel debugger, so a build carrying the `serialdebug`
plugin, booted with `pebuild run --serial`, prints a timeline of the bring-up:

```
peshell: start
peshell: stage: Starting services...
peshell: startup stage pre-shell
peshell: stage: Registering shell components...
peshell: stage: Setting up the Start menu...
peshell: stage: Preparing the Control Panel...
peshell: stage: Applying the visual style...
peshell: stage: Launching the desktop...
peshell: relaunching explorer                      (only if Explorer exits)
peshell: desktop gone, falling back to cmd.exe
```

Stage lines come from `splash_status`, the one place stages are announced, so whatever the splash
tells the user the debugger gets too and the trace cannot fall behind as stages are added. The
phases with no splash message report separately: process entry, each startup stage (including
`skipped, no perun.dll` and `skipped, perun.dll has no PerunRunStage`, which are otherwise silent
no-ops), Explorer relaunch, and the `cmd.exe` fallback.

A boot that stops therefore names the stage it stopped in, and `self-register` lines name any
component that failed to register. All of it is ASCII: the output may be read on a console in any
code page, and a build from double-byte media would otherwise turn it into mojibake.

Everything printed here is bounded — the buffers are sized off the constants that bound their
inputs (`g_status`, `SELFREG_NAME`) rather than off a worst case measured today, because
`wsprintfA` does not truncate and those constants are meant to be raised.

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

## Wallpaper

`peshell` also applies the wallpaper the registry names, for the same reason it applies the
visual style: a real Windows applies the per-user desktop settings during logon, and a PE has no
winlogon to run that. Left alone, the values sit in the hive unread and the desktop comes up on
a flat colour.

**The contract** is the standard three values under `HKCU\Control Panel\Desktop` — `Wallpaper`,
`WallpaperStyle`, `TileWallpaper` — written by whichever plugin wants a wallpaper (see
`wallpaper`). Nothing here is image-specific. With no `Wallpaper` value it returns before
touching COM, so a build without such a plugin pays nothing. `Wallpaper` is a `REG_EXPAND_SZ`
and peshell expands it: **the system drive is not `C:`**.

The apply goes through `IActiveDesktop` — the interface the Display Properties dialog uses —
rather than `SystemParametersInfo`, for two reasons: the classic path takes bitmaps only, so a
JPEG could not be shown at all, and `ApplyChanges` also generates the `Desktop.htt` the surface
needs. It follows that the wallpaper plugin must also stage and register an HTML engine; that is
its business, not this plugin's, and it declares it through the self-registration contract above.

**Where it happens matters.** The apply runs after the shell is up — the desktop window has to
exist for it to stick — but before the splash closes, so the wallpaper arrives together with the
desktop instead of flipping in over it a moment later.

## Splash

The "Please wait…" window mimics the Windows startup/shutdown status dialog: a title bar,
the Windows branding banner (loaded from the source's `msgina.dll`) across the top, a thin
running blue↔orange bar beneath it, and the current phase text. It runs on its own thread
so it keeps animating during the bring-up. The window sizes itself from the bitmap it loads.

### Which banner

Server media keeps its workstation-branded banner at resource 101 and carries the
edition-branded ones elsewhere, so reading 101 unconditionally splashes a Server 2003 build
under a Windows XP logo. peshell asks for **130** (Standard Edition) when `GetVersionEx`
reports a non-workstation product and 101 otherwise, falling back to 101, the only one every
source has. Standard because the environment reports an empty `ProductSuite`; the other
editions sit at 131–135 and 144–147 if that ever needs selecting.

## Shutdown

`/minint` has no Winlogon, so the shell's "Turn Off Computer" call to `ExitWindowsEx`
routes to a shutdown coordinator that isn't there and fails silently — nothing powers off.
`peshut.dll` fixes this: it is loaded into shell processes via `AppInit_DLLs` and, on load,
inline-patches `user32!ExitWindowsEx` so any shutdown/restart request instead drives the
kernel's ACPI path directly (`NtShutdownSystem`) — the same primitive as the `poweroff`
plugin. Restart maps to reboot, everything else to power-off.

### The patch, per architecture

The hook fully replaces `ExitWindowsEx` and never calls the original, so the prologue bytes
it overwrites are never executed and no trampoline is needed. That is what lets the patch be
whatever length each architecture requires — a hook that had to preserve the original could
not simply consume 14 bytes of it.

- **x86** writes the classic 5-byte `jmp rel32`. The XP prologue is exactly 5 bytes
  (`mov edi,edi; push ebp; mov ebp,esp`), so nothing after it is disturbed.
- **amd64** writes `jmp qword ptr [rip+0]` followed by the 8-byte target, 14 bytes in all.
  A `rel32` cannot be used: the two modules are placed independently in a 64-bit address
  space and are routinely further apart than a 32-bit displacement can express, so it would
  silently encode a truncated offset and corrupt the function in every process that loads
  the DLL. The amd64 entry is 14+ bytes of register-saving prologue and has 16 bytes of
  `nop` padding ahead of it, so the write stays inside the function either way.

`WINAPI` is `__stdcall` on x86 and a no-op on amd64, where the two arguments arrive in
`ecx`/`edx` exactly as the real `ExitWindowsEx` reads them — so callers jump straight in on
both, and the replacement needs no signature change.

### Why the build is freestanding

`AppInit_DLLs` is loaded by user32 from its own `DLL_PROCESS_ATTACH`, **before the host
process's C runtime is initialized**. MinGW's startup stubs pull in `msvcrt` even though
nothing in `peshut.c` calls it, and touching an uninitialized CRT there aborts the process
with `R6030`. Both architectures therefore link with `-nostdlib -nodefaultlibs` and
`DllMain` as the entry point, importing only `kernel32` and `advapi32`; the source is pure
Win32 API, so it loses nothing. Relocations are unaffected — `HAS_RELOC` is set and the
`.reloc` section survives on both — so the DLL can still be rebased, which matters for one
injected into every process.

x86 was once left on the stock link line, which worked only because XP's `msvcrt` tolerated
it; on Server 2003 the same binary aborts every GUI process at shell start. The giveaway is
an empty `Program:` field in the dialog — `_pgmptr` is filled in during the CRT init that
never ran.

The entry point has to be named once the startup stubs are gone, and only i386 decorates it
(`_DllMain@12`). A name the linker cannot find is merely a **warning**: it leaves the start
address at 0 and emits a DLL that links cleanly but cannot load, so check `objdump -f` after
touching that line.

The `VERSIONINFO` object on the link line does not compromise any of this. A `windres` output
is a lone `.rsrc` section — data, no code and no CRT reference — so it adds a section and
changes nothing else; the entry-point RVA is identical with and without it.

**A correction worth recording**, because it was believed and acted on: the `R6030` was
*not* caused by the truncated `rel32`. That was assumed from the fact that removing peshut
made the crash go away, and never measured — a correctly encoded 14-byte jump reproduced it
exactly. Two separate bugs sat on top of each other, and fixing only the visible one left
the crash in place.

**Log Off** is meaningless here — only the SYSTEM user exists — and it appears on two separate
surfaces, each needing its own HKLM policy: `StartMenuLogoff` removes the modern menu's button,
`NoLogoff` the shutdown dialog's row. Two traps:

- `NoLogoff` is honoured by *msgina's* dialog. If msgina can't load, shell32 falls back to its own
  dialog, which ignores the policy — and msgina reaches it through `odbc32`, which loads
  **`odbcint.dll`** by name. That DLL is invisible to any import closure, so it is staged
  explicitly by `base.inf`. Dropping it there silently un-removes Log Off here.
- `StartMenuLogoff` must be `1`. Setting it to `2` forces the button back *on* rather than
  removing it more firmly.

## Visual effects

The desktop uses the classic no-frills look (no animations/fades/shadows). These are
win32k *session* settings: the baked `Control Panel\Desktop\UserPreferencesMask` and
`WindowMetrics\MinAnimate` are **not** enough on their own — during shell init XP runs an
"apply recommended visual effects" pass that recomputes the mask and overwrites the live
`HKU\.DEFAULT` copy. The `Explorer\VisualEffects\<effect>\DefaultApplied=1` flags (plus
`VisualFXSetting=3`) tell XP those defaults are already applied, suppressing the recompute
so the baked values survive. Both halves are required.

## Modern Start menu

The desktop uses the **modern two-column XP Start panel**, not the classic 2000-style menu. A
`/minint` SOFTWARE hive doesn't come configured for it (GUI-mode setup would do that), so three
things are needed, all in `explorer-shell.inf`:

- **`duser.dll`** — the DirectUI runtime the panel is drawn with. shell32 loads it by name at
  runtime (it's in no import closure), so it's a `[Closure]` seed.
- **The two Start-panel COM objects** — CLSIDs `{4622AD11-…}` (the Start Menu band) and
  `{A2A9545D-…}` (the "Pin to Start menu" handler), both served by shell32. Their
  `InProcServer32` registration is written in `[Software.AddReg]`; without it the panel can't
  instantiate and the shell falls back to the classic menu.
- **The selection** — `DefaultStartPanelOff` under `…\Explorer\StartMenu\StartPanel` (read
  HKCU-then-HKLM): `1` selects the classic menu, `0` or *absent* the modern panel. Since a
  `/minint` hive has no `StartMenu\StartPanel` key at all, the modern panel is what you get
  unless the `classic_start_menu` option asks otherwise. The `NoSimpleStartMenu` policy (which
  would force classic and hide the choice) is deliberately not set. Note there is no
  `Start_ShowClassicMenu` value — that name appears in no shell binary and does nothing.

### Right-column / panel cleanup

The panel is trimmed to what makes sense in a PE via per-user
`Explorer\Advanced` flags: My Music / My Pictures / Printers hidden (`Start_ShowMyMusic` /
`Start_ShowMyPics` / `Start_ShowPrinters` = 0). Because the modern menu hides My Computer and My
Documents from the *desktop* by default (it relocates them into the menu), they're kept on the
desktop explicitly under `HideDesktopIcons\NewStartPanel` (CLSID = 0 = shown).

### Internet / E-mail

The panel ships two default *pinned* items — Internet (the default browser)
and E-mail (the default mail client) — shown unconditionally, with no policy to remove them (there
is no client-registration or restriction lever; confirmed by reverse-engineering shell32). The
pinned list is a serialized pidl stream shell32 streams in and out of
`HKCU\…\Explorer\StartPage\Favorites` via `SHOpenRegStream2W`. Writing a single `0xFF` byte there
is what shell32 itself writes for an empty list (it is the stream's end-of-list terminator), so the
two defaults are not seeded and the list stays available for our own items.

The `0xFF` marker is **not sufficient on its own**: `Explorer\Advanced\StartMenuInit` must also say
the panel has been initialized (value `2`, what the shell writes after its own first run). Without
it the shell treats the profile as fresh and re-seeds both default pins straight over the marker.
The two values are only correct together — removing either brings Internet and E-mail back.

### First-run auto-open

Left alone, the panel opens itself the first time the desktop comes up.
The gate is `Explorer\Advanced\StartButtonBalloonTip`: explorer opens the panel while the value is
below `2`, then stamps `2` itself, so pre-seeding `2` suppresses it. The name is misleading — it
reads as if it controlled a balloon tip, and one function forks on `IsStartPanelOn()` to decide
between opening the panel and showing a balloon, which is how it came by that name.

This is the **second** thing depending on `StartMenuInit=2`: below that value, explorer's first-run
initialization deletes both this value and the `StartPage` key above, so the pinned-list marker and
the auto-open gate stand or fall together.

### Top-level items are pinned

The panel does not render Start Menu root shortcuts, so a contract
entry with a leading backslash (see [Start-menu items](#start-menu-items)) is *also* pinned: peshell
drives shell32's "Pin to Start menu" object — `IStartMenuPin::Modify(NULL, pidl)` on the same CLSID
registered above — once per item, in contract order, right after the menu is built and before
Explorer starts. That call is registry I/O plus a change notification: it needs no window, no data
object and no running shell, and the panel re-reads the list every time it opens, so nothing has to
be notified. Letting shell32 author the entries (rather than writing the blob by hand) also stores
each one relative to a known folder, so no drive letter is baked in.

### Account picture

The panel's header can carry the account picture (the tile beside the user name). Explorer draws it
from `<Common AppData>\Microsoft\User Account Pictures\<user>.bmp` and draws nothing at all if the
file is missing, so `peshell` puts one there at boot. **The bitmap comes from another plugin**, via
a one-value contract:

    HKLM\SOFTWARE\pe-builder    UserPicture = REG_EXPAND_SZ   path to a staged .bmp

`peshell` expands that path and copies the file to the name Explorer will ask for. No value, no
copy — which is the state of a build with no visual style, since the tile cannot render there
anyway. `luna` is the plugin that declares one today; whether the tile is *drawn* is decided by its
`logon_type` option (see luna's README § Logon type).

The copy is unconditional whenever the value is set: whether the tile is actually *drawn* is
decided by Explorer, not here, so `peshell` writes the file and lets the shell decide. A few KB on
the ramdrive is cheaper than teaching the shell launcher which gates the panel applies.

Two details decide whether the copy lands:

- **The file name is the account, not `%USERNAME%`.** Explorer builds it from `GetUserName`, i.e.
  the process token — `SYSTEM` in a PE — while `%USERNAME%` is set to "Default User" for the
  profile. `peshell` asks the same way rather than assuming, so the two always agree.
- **It runs after the service wait**, with the Start-menu step, not with the profile setup that
  creates the folders. The account lookup goes through the LSA, which is not reachable that early;
  run there it fails, and the tile silently never appears. It still has to precede Explorer, which
  reads the picture once when it builds the panel and caches the bitmap — a file that appears later
  is not picked up until the shell restarts.

Clicking the tile does nothing, deliberately. Explorer's handler calls shell32's
`SHRunControlPanel` with `nusrmgr.cpl ,initialTask=ChangePicture` — the User Accounts applet on
its "change your picture" task. That applet manages Windows accounts against the SAM, and this PE
runs empty SAM/SECURITY hives, so it isn't staged; the call fails silently, which is a better
outcome than opening an applet onto a database that isn't there. The cursor still hot-tracks to a
hand over the tile, since that is the panel's own rendering.

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