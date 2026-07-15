// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* peshut.dll — makes the shell's "Turn Off Computer" actually power the machine off in a /minint
 * PE (which has no Winlogon, so ExitWindowsEx would just fail silently). Loaded into shell
 * processes via AppInit_DLLs; on load it inline-patches user32!ExitWindowsEx to drive the ACPI
 * power-off directly. See README.md § Shutdown. */
#include <windows.h>

typedef LONG (WINAPI *NtShutdown_t)(int);   /* ShutdownReboot=1, ShutdownPowerOff=2 */

/* Replacement for ExitWindowsEx: enable SeShutdownPrivilege via its fixed well-known LUID (works
 * even with an uninitialized LSA policy DB) and drive the kernel shutdown directly. It matches
 * ExitWindowsEx's __stdcall(UINT,DWORD) signature so callers can jump straight in. Never returns
 * on success. */
static BOOL WINAPI hook_ExitWindowsEx(UINT flags, DWORD reason){
    (void)reason;
    HANDLE tok; TOKEN_PRIVILEGES tp;
    tp.PrivilegeCount=1; tp.Privileges[0].Attributes=SE_PRIVILEGE_ENABLED;
    tp.Privileges[0].Luid.LowPart=19; tp.Privileges[0].Luid.HighPart=0;   /* SE_SHUTDOWN_PRIVILEGE */
    if(OpenProcessToken(GetCurrentProcess(),TOKEN_ADJUST_PRIVILEGES|TOKEN_QUERY,&tok)){
        AdjustTokenPrivileges(tok,FALSE,&tp,0,NULL,NULL); CloseHandle(tok);
    }
    NtShutdown_t nt=(NtShutdown_t)GetProcAddress(GetModuleHandleA("ntdll.dll"),"NtShutdownSystem");
    if(nt) nt((flags & EWX_REBOOT) ? 1 : 2);   /* restart vs power off */
    return TRUE;
}

/* Overwrite the first 5 bytes of user32!ExitWindowsEx with a jmp to hook_ExitWindowsEx. We never
 * call the original (we fully replace it), so no trampoline is needed. The XP prologue is exactly
 * 5 bytes (mov edi,edi; push ebp; mov ebp,esp), so nothing after it is disturbed. */
static void install_hook(void){
    BYTE *target=(BYTE*)GetProcAddress(GetModuleHandleA("user32.dll"),"ExitWindowsEx");
    if(!target) return;
    DWORD old;
    if(!VirtualProtect(target,5,PAGE_EXECUTE_READWRITE,&old)) return;
    target[0]=0xE9;                                              /* jmp rel32 */
    *(DWORD*)(target+1)=(DWORD)((BYTE*)hook_ExitWindowsEx-(target+5));
    VirtualProtect(target,5,old,&old);
    FlushInstructionCache(GetCurrentProcess(),target,5);
}

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved){
    (void)reserved;
    if(reason==DLL_PROCESS_ATTACH){ DisableThreadLibraryCalls(inst); install_hook(); }
    return TRUE;
}
