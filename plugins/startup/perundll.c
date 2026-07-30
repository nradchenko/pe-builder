/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * perun.dll — the DLL front end. It is nothing but an entry point: the walk itself is
 * perun.c, which peshell reaches through the exports declared in perun.h.
 *
 * Unlike peshut.dll next door, this one links the CRT. peshut has to be freestanding for a
 * reason specific to it: AppInit_DLLs injects it into every process that loads user32, and a
 * second CRT initialization inside an arbitrary host is what produces the R6030 on Server
 * 2003. None of that applies here — perun.dll is loaded deliberately, by one caller, through
 * LoadLibrary. Worth stating because the neighbouring Makefile does the opposite and the
 * reason is invisible from the code.
 */

#include <windows.h>

#include "perun.h"

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved)
{
    (void)reserved;

    if (reason == DLL_PROCESS_ATTACH)
        DisableThreadLibraryCalls(inst);
    return TRUE;
}
