/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * pesuite — a boot-start driver that sets VER_SUITE_TERMINAL in the shared SuiteMask.
 *
 * Why this exists: the Win32 subsystem gates the whole WinStation/terminal-services
 * session subsystem on one bit — winsrv!UserServerDllInitialization tests
 * `SharedUserData->SuiteMask & VER_SUITE_TERMINAL` and, when it is clear, skips the
 * init that would create \SmSsWinStationApiPort and Global\TermSrvReadyEvent. winlogon
 * reads the same bit. Without it there is no console session, so nothing that needs one
 * (the theme server / visual styles, terminal services) can come up.
 *
 * Normally the kernel sets that bit during its product init, from the registry value
 * Control\Terminal Server\TSEnabled. But it reads Setup\SetupType first, and when that
 * is 1 or 4 ("setup in progress") it skips the entire product-init block — SuiteMask is
 * never written and stays 0. A PE needs SetupType=1: it is what puts winlogon in setup
 * mode, which is what runs Setup\CmdLine (the shell). The kernel and winlogon test the
 * same {1,4} set, so the shell hand-off and the suite bit cannot both be had from that
 * value. This driver decouples them: it sets the bit directly, leaving SetupType alone.
 *
 * How: KUSER_SHARED_DATA is mapped read-only into user mode at 0x7ffe0000, and
 * read/write into the kernel's address space at 0xffdf0000 (x86). A boot-start driver's
 * DriverEntry runs during IoInitSystem, in kernel Phase 1 — after the kernel's own
 * (skipped) product init, and before the session manager starts, so before smss spawns
 * csrss/winsrv and winlogon. The write therefore lands before every reader of the bit.
 *
 * The driver does nothing else: no device, no dispatch table, no unload. It is a
 * one-shot boot-time poke.
 */
/* Target Windows XP (literals: the symbolic NTDDI_* names aren't defined until
 * sdkddkver.h consumes these — 0x0501 = _WIN32_WINNT_WINXP, 0x05010000 = NTDDI_WINXP). */
#define _WIN32_WINNT  0x0501
#define NTDDI_VERSION 0x05010000
#include <sdkddkver.h>

/* mingw-w64's ddk wdm.h guards a Vista-only field on NTDDI_WINVISTA — a typo for
 * NTDDI_VISTA (defined nowhere), so at a sub-Vista target the field compiles in
 * against an undefined struct. Alias the typo to the real macro. */
#ifndef NTDDI_WINVISTA
#define NTDDI_WINVISTA NTDDI_VISTA
#endif

#include <ntddk.h>

/* KI_USER_SHARED_DATA (0xffdf0000) — the kernel-mode, writable alias of the page user
 * mode sees read-only at 0x7ffe0000 — comes from the ddk's wdm.h. SuiteMask sits at
 * +0x2d0: verified against this build's ntoskrnl, which writes the neighbouring
 * KUSER_SHARED_DATA fields (NtProductType +0x264, ProductTypeIsValid +0x268,
 * NXSupportPolicy +0x2d5, ActiveConsoleId +0x2d8) at the documented offsets. */
#define SUITEMASK_OFFSET 0x2d0

#ifndef VER_SUITE_TERMINAL
#define VER_SUITE_TERMINAL 0x00000010
#endif
#ifndef VER_SUITE_SINGLEUSERTS
#define VER_SUITE_SINGLEUSERTS 0x00000100
#endif

/* Reproduce what the kernel's skipped product init would have computed from the registry it
 * never read, so the running system matches a real XP Pro's SuiteMask (0x110) exactly:
 *   VER_SUITE_TERMINAL     <- Control\Terminal Server\TSEnabled = 1   (the gate winsrv tests)
 *   VER_SUITE_SINGLEUSERTS <- Control\Terminal Server\TSAppCompat = 0 (workstation, i.e. one
 *                             session at a time — what XP Pro reports, and the mode termsrv
 *                             expects on a workstation)
 * Both values are already staged in the SYSTEM hive; this driver exists only because the
 * SetupType=1 boot path means nothing ever reads them. */
#define PESUITE_MASK (VER_SUITE_TERMINAL | VER_SUITE_SINGLEUSERTS)

/* DRIVER_INITIALIZE gives DriverEntry the stdcall convention the kernel calls it with;
 * a plain cdecl definition would leave the kernel's arguments on the stack. */
DRIVER_INITIALIZE DriverEntry;

NTSTATUS NTAPI DriverEntry(PDRIVER_OBJECT drv, PUNICODE_STRING regPath)
{
    volatile ULONG *suiteMask = (volatile ULONG *)(KI_USER_SHARED_DATA + SUITEMASK_OFFSET);

    UNREFERENCED_PARAMETER(drv);
    UNREFERENCED_PARAMETER(regPath);

    *suiteMask |= PESUITE_MASK;

    /* Returning success with no device and no dispatch routines keeps the driver
     * object loaded and inert; nothing ever sends it an IRP. */
    return STATUS_SUCCESS;
}
