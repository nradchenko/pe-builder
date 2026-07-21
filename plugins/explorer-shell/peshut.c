// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* peshut.dll — makes the shell's "Turn Off Computer" actually power the machine off in a /minint
 * PE (which has no Winlogon, so ExitWindowsEx would just fail silently). Loaded into shell
 * processes via AppInit_DLLs; on load it inline-patches user32!ExitWindowsEx to drive the ACPI
 * power-off directly. Built for both architectures — the patch encoding differs, see
 * write_jump below. See README.md § Shutdown. */
#include <windows.h>

typedef LONG (WINAPI *NtShutdown_t)(int);   /* ShutdownReboot=1, ShutdownPowerOff=2 */

/* Replacement for ExitWindowsEx: enable SeShutdownPrivilege via its fixed well-known LUID (works
 * even with an uninitialized LSA policy DB) and drive the kernel shutdown directly. It matches
 * ExitWindowsEx's signature so callers can jump straight in, on either architecture: WINAPI is
 * __stdcall on x86 and a no-op on x64, where the two arguments arrive in ecx/edx exactly as the
 * real ExitWindowsEx reads them. Never returns on success. */
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

/* Overwrite the entry of user32!ExitWindowsEx with a jump to hook_ExitWindowsEx. Both variants
 * rely on fully replacing the function and never calling the original, so the prologue bytes
 * the patch overwrites are never executed and no trampoline is needed. They are written out
 * separately rather than sharing a helper so the x86 path stays exactly the code that was
 * verified by booting. See README.md § Shutdown for the encodings and why they differ. */
#ifdef _WIN64
/* amd64: jmp qword ptr [rip+0] followed by the 8-byte target, so the jump reaches anywhere in
 * the address space — a rel32 cannot, and would silently truncate. The entry is 14+ bytes of
 * register-saving prologue (sub rsp,0x48; mov [rsp+0x50],rbx; ...) with 16 bytes of nop
 * padding ahead of it, so the write stays inside this function. */
static void install_hook(void){
    BYTE *target=(BYTE*)GetProcAddress(GetModuleHandleA("user32.dll"),"ExitWindowsEx");
    if(!target) return;
    DWORD old;
    if(!VirtualProtect(target,14,PAGE_EXECUTE_READWRITE,&old)) return;
    target[0]=0xFF; target[1]=0x25;                              /* jmp qword ptr [rip+disp32] */
    *(DWORD*)(target+2)=0;                                       /* disp32=0 => operand follows */
    *(ULONGLONG*)(target+6)=(ULONGLONG)(ULONG_PTR)hook_ExitWindowsEx;
    VirtualProtect(target,14,old,&old);
    FlushInstructionCache(GetCurrentProcess(),target,14);
}
#else
/* x86: the classic 5-byte relative jump. The XP prologue is exactly 5 bytes
 * (mov edi,edi; push ebp; mov ebp,esp), so nothing after it is disturbed. */
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
#endif

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved){
    (void)reserved;
    if(reason==DLL_PROCESS_ATTACH){ DisableThreadLibraryCalls(inst); install_hook(); }
    return TRUE;
}
