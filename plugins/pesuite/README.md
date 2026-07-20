<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# pesuite

Sets the product **SuiteMask** bit that a PE otherwise boots without, so the Win32 subsystem will
bring up its WinStation/session machinery. Off by default.

The plugin ships a tiny kernel driver, `pesuite.sys`, compiled from source at build time by the
`[Build]` pre-pass. Its whole body is one OR into a shared kernel page.

**On its own this plugin does nothing observable** — it only opens the gate. See § What it does not
do.

## Pieces

| File | What it is |
|---|---|
| `pesuite.inf` | stages the driver and defines its service entry |
| `pesuite.c` | the driver: `DriverEntry` sets the bit and returns |
| `Makefile` | cross-compiles it with MinGW-w64 (no WDK, no MSVC) |

## Why the bit is missing

`winsrv` gates the **entire** WinStation/terminal-services subsystem on one bit of
`SharedUserData->SuiteMask` — `VER_SUITE_TERMINAL`. With it clear, winsrv skips that init, so the
PE has no console session and nothing session-gated (the theme server and visual styles, fast user
switching, terminal services) has anything to attach to. `winlogon` reads the same bit.

The kernel normally sets it during product init, from `Control\Terminal Server\TSEnabled`. But it
reads `SYSTEM\Setup\SetupType` **first**, and when that is `1` or `4` ("setup in progress") it skips
the whole product-init block — `SuiteMask` is never written and stays `0`. Measured in the PE before
this driver existed: `SuiteMask=0x000`, `ProductTypeIsValid=0` — the exact signature of that skip.
`TSEnabled=1` was staged correctly all along; nothing ever read it.

**It is a registry cause, not `/minint`.** `/minint` is a separate mechanism (smss's
`Control\MiniNT` key) and is not what closes this gate. That distinction cost real time; do not
re-derive it.

## Why not just change SetupType

Because the same value buys the shell. `winlogon` tests the **same `{1,4}` set** to decide it is in
setup mode, and setup mode is what runs `Setup\CmdLine` — our `peshell.exe`. So:

- `SetupType ∈ {1,4}` → the shell hand-off works, SuiteMask is forfeit.
- `SetupType = 2` → the kernel computes `SuiteMask=0x110` natively and this driver becomes
  redundant, but the shell must move to `Winlogon\Shell` + autologon, which needs a populated SAM
  (ours is deliberately empty).

The two are coupled through one value. This driver decouples them: it supplies the bit directly and
leaves `SetupType` alone. **Keeping `SetupType=1` is deliberate**: it preserves the `Setup\CmdLine`
shell hand-off, keeps the PE running as SYSTEM (so `HKCU` is `HKU\.DEFAULT`, where the shell's
per-user configuration already goes), and needs no SAM or profile work. The cost is this driver,
which is small and proven.

## How it sets it

`KUSER_SHARED_DATA` is mapped read-only into user mode at `0x7ffe0000` and **read/write into the
kernel's address space at `0xffdf0000`** (x86). The driver ORs `0x110` into `*(ULONG*)0xffdf02d0`:

| Bit | Name | Why |
|---|---|---|
| `0x10` | `VER_SUITE_TERMINAL` | the bit winsrv gates on; termsrv checks it too |
| `0x100` | `VER_SUITE_SINGLEUSERTS` | together, exact parity with a real XP Pro (`0x110`) |

No device, no dispatch table, no unload — a one-shot boot-time poke.

## Why `Start=1`, not `Start=0`

In `/minint` the boot-start set is whatever setupldr preloads from `txtsetup.sif`, so a
**registry-only `Start=0` entry is never loaded** (that is why the staged VolSnap `Start=0` entry is
inert). A `Start=1` driver *is* loaded from its registry key by the kernel's system-driver pass,
which still runs in Phase 1 — before smss spawns csrss/winsrv and winlogon — so the write lands
ahead of every reader of the bit.

That ordering is a property of the kernel's own system-driver pass, so it holds for this plugin on
its own. It was first observed on `termdd` — also `Start=1` and registry-only — which is loaded the
same way; that driver now ships with **terminal-services**, so in a build with only pesuite enabled
the corroborating example is absent, but the argument does not rest on it.

If it ever proves too late, the escalation is `[AddLine] "txtsetup.sif","BusExtenders.Load"`.

## Build

MinGW-w64 builds a loadable XP kernel driver with no WDK and no MSVC.

The driver source is architecture-neutral; only the toolchain differs. `make ARCH=i386`
(the default) and `make ARCH=amd64` select it, and the plugin's `[Build.<arch>]` sections
name each architecture's compiler and pass the matching `ARCH` — so the presence check
tests the compiler actually needed rather than a superset. The whole per-architecture
difference is one switch at the top of the `Makefile`: the toolchain triple, the arch
define (`_X86_` / `_AMD64_`), the entry-point name (x64 has no `_…@8` stdcall
decoration), the native subsystem version (5.01 / 5.02), and the DDK include path. An
`ARCH` the recipe has no flags for is an error, not a fallback — a driver silently built
for the wrong architecture would not load, and would look like a driver that did nothing.

**Proven with:**

| | Version |
|---|---|
| Compiler (i386) | `i686-w64-mingw32-gcc (GCC) 13-win32` — Debian/Ubuntu `gcc-mingw-w64-i686` 13.2.0 |
| Compiler (amd64) | `x86_64-w64-mingw32-gcc (GCC) 13-win32` — Debian/Ubuntu `gcc-mingw-w64-x86-64` |
| Linker | `GNU ld (GNU Binutils) 2.41.90.20240122` — `binutils-mingw-w64-i686` / `-x86-64` |
| Kernel it loads against | `ntoskrnl.exe` **5.1.2600.5512** (`xpsp.080413-2111`), i.e. XP SP3 — staged from the source's `ntkrnlmp.exe` (the base layer installs the multiprocessor kernel under the uniprocessor name). Proven on both the Russian and English VL sources: at this version their kernels differ only in the localized `.rsrc` section — every code and data section is byte-identical. |

Those are the versions it was proven on, not the versions it is pinned to — **nothing here is
hardcoded to that kernel by address.** The base comes from the DDK's `KI_USER_SHARED_DATA`, which
the header defines per architecture (`0xffdf0000` on x86, `0xfffff78000000000` on x64), so it is
the architecture's ABI rather than anything RE'd. The one literal, `SuiteMask` at **`+0x2d0`**, is
corroborated by the DDK's own `KUSER_SHARED_DATA`, whose layout carries no version conditionals
before that field — and user mode reads the same page at `0x7ffe0000`, which makes those early
offsets a public contract that does not move within a kernel line. The driver should therefore
hold across XP service packs and editions.

That `+0x2d0` holds on x64 as well, checked rather than assumed: the x64 kernel's own product
init reads that address, ORs in `0x10`, sets bit 8, and writes it back — the same
`VER_SUITE_TERMINAL | VER_SUITE_SINGLEUSERTS` this driver sets, at the same offset. The
neighbouring fields the offset was originally corroborated against (`NtProductType +0x264`,
`ProductTypeIsValid +0x268`, `NXSupportPolicy +0x2d5`, `ActiveConsoleId +0x2d8`) sit where they do
on x86. On an XP x64 source the driver has been built, staged system-start, booted, and confirmed
to have set the bit.

What is version-bound is the rest of the chain, not this: `Luna.msstyles` is XP-only, and the
shell ordinals the applier calls are per-build export tables.

The gotchas that matter live in `pesuite.c`'s own comments, where the code gives the context —
briefly:

- **`DRIVER_INITIALIZE`/`NTAPI` on `DriverEntry` is mandatory.** Without it gcc emits a cdecl `ret`
  while the kernel calls it stdcall; the linker only *warns*. Want `ret $0x8`.
- **The PE checksum must be valid** — the XP image loader validates it. The binutils above writes
  one at link time (verified: the built `pesuite.sys` carries a non-zero `CheckSum`), so the
  Makefile only *guards* against an older binutils that would leave it zero, failing the build with
  a clear message rather than shipping a driver that cannot load.
- mingw's ddk header guards a Vista-only field on a typo'd macro; `pesuite.c` aliases it.

## What it does not do

Setting the bit lets winsrv *run* its WinStation init — which then **blocks**, waiting for the
terminal-services stack to create the session port. Without that stack this plugin changes nothing
you can see. **terminal-services** depends on this one and completes the chain; see its README.