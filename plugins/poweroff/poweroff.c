// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* poweroff.c — direct ACPI poweroff for a /minint PE: enable SeShutdownPrivilege,
 * then call NtShutdownSystem(ShutdownPowerOff), which drives the kernel shutdown
 * into the HAL's ACPI S5 without the winlogon coordinator a PE does not run.
 * See README.md for why that path, and § Exit codes for what a return means. */
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
    /* SeShutdownPrivilege's fixed well-known LUID {19, 0}, written directly rather than
     * looked up: LookupPrivilegeValue needs the LSA policy DB, which a minimal PE may
     * never have initialized. See README.md § Enabling the privilege without LSA. */
    tp.Privileges[0].Luid.LowPart = 19;   /* SE_SHUTDOWN_PRIVILEGE */
    tp.Privileges[0].Luid.HighPart = 0;
    /* Succeeds even when it adjusted nothing, so the verdict comes from GetLastError. */
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