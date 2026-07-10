// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* poweroff.c — direct ACPI poweroff for a /minint PE.
 *
 * Enables SeShutdownPrivilege via its fixed well-known LUID (so it works even
 * when the LSA policy DB is uninitialized — LookupPrivilegeValue would fail
 * there) and calls NtShutdownSystem(ShutdownPowerOff), bypassing the winlogon
 * shutdown coordinator (the RPC path that returns "device not ready" without
 * RPCSS) and the power policy (the inert ACPI button). It drives the kernel
 * shutdown directly into the HAL's ACPI S5.
 *
 * On success the machine powers off and this never returns. If it returns,
 * the exit code carries the reason:
 *   1  OpenProcessToken failed
 *   3  SeShutdownPrivilege not held by the token (adjust failed)
 *   4  NtShutdownSystem export not found
 *   <negative>  the NTSTATUS from NtShutdownSystem (the kernel refused to
 *               power off — a HAL/ACPI dig, not a wiring problem)
 *
 * Build (32-bit, XP target):
 *   i686-w64-mingw32-gcc -O2 -s -o poweroff.exe poweroff.c -ladvapi32
 */
#include <windows.h>

typedef LONG NTSTATUS;
typedef enum { ShutdownNoReboot = 0, ShutdownReboot = 1, ShutdownPowerOff = 2 }
        SHUTDOWN_ACTION;
typedef NTSTATUS (WINAPI *NtShutdownSystem_t)(SHUTDOWN_ACTION);

int main(void)
{
    HANDLE tok;
    TOKEN_PRIVILEGES tp = {0};
    NtShutdownSystem_t NtShutdownSystem;

    if (!OpenProcessToken(GetCurrentProcess(),
                          TOKEN_ADJUST_PRIVILEGES | TOKEN_QUERY, &tok))
        return 1;
    tp.PrivilegeCount = 1;
    tp.Privileges[0].Attributes = SE_PRIVILEGE_ENABLED;
    /* SeShutdownPrivilege has a fixed well-known LUID {19, 0}; use it directly
     * rather than LookupPrivilegeValue, which needs the LSA policy DB — that may
     * be uninitialized in a minimal PE. AdjustTokenPrivileges acts on the token
     * (a kernel object), so it works regardless of LSA health. */
    tp.Privileges[0].Luid.LowPart = 19;   /* SE_SHUTDOWN_PRIVILEGE */
    tp.Privileges[0].Luid.HighPart = 0;
    AdjustTokenPrivileges(tok, FALSE, &tp, 0, NULL, NULL);
    if (GetLastError() != ERROR_SUCCESS)
        return 3;

    NtShutdownSystem = (NtShutdownSystem_t)GetProcAddress(
        GetModuleHandle("ntdll.dll"), "NtShutdownSystem");
    if (!NtShutdownSystem)
        return 4;

    /* Powers off and never returns on success; returns the NTSTATUS on failure. */
    return (int)NtShutdownSystem(ShutdownPowerOff);
}