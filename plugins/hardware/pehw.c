/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * pehw — the hardware configuration a PE has to perform at run time, because it runs none of
 * the setup passes Windows would have. One binary with subcommands; `devices` is the PnP
 * driver-install pass. Declared as a startup run-list task, and re-runnable by hand.
 * See README.md.
 */

#include <windows.h>
#include <setupapi.h>
#include <stdio.h>
#include <stdlib.h>

/* COBJMACROS gives the C call form (INetCfg_Initialize(p, ...)) for interfaces the headers
 * otherwise only spell as C++ methods. initguid.h has to precede any header that declares a
 * GUID we use: it turns DEFINE_GUID from an extern reference into a definition, so the CLSID
 * and IIDs below are emitted here rather than pulled from a library that may not carry them. */
#define COBJMACROS
#include <objbase.h>
#include <iphlpapi.h>
#include <initguid.h>
#include <devguid.h>
#include <netcfgx.h>
/* netcon.h is the *connection* half of the network API (INetConnectionManager, the object the
 * shell's network UI talks to), and netcfgx.h does not pull it in — the two describe different
 * things: netcfgx configures components, netcon enumerates the connections that result. */
#include <netcon.h>

/* netcon.h declares these two as plain externs rather than through DEFINE_GUID, and no MinGW-w64
 * import library defines them — so initguid.h cannot emit them and the link fails. Defined here
 * instead. The values are not from memory: they are read back from a reference PE's registry,
 * where the class key names itself "Network Connection Manager Class" and the interface key
 * "INetConnectionManager". */
DEFINE_GUID(pehw_CLSID_ConnectionManager,
            0xBA126AD1, 0x2166, 0x11D1, 0xB1, 0xD0, 0x00, 0x80, 0x5F, 0xC1, 0x27, 0x0E);
DEFINE_GUID(pehw_IID_INetConnectionManager,
            0xC08956A2, 0x1CD3, 0x11D1, 0xB1, 0xC5, 0x00, 0x80, 0x5F, 0xC1, 0x27, 0x0E);

/* IID_IUnknown and GUID_NULL are externs for the same reason as the two above. The probe below
 * asks for the most basic interface, so that it tests activation and nothing else; GUID_NULL is
 * what it compares a connection's declared UI class against before trying to create it. */
DEFINE_GUID(pehw_IID_IUnknown,
            0x00000000, 0x0000, 0x0000, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46);
DEFINE_GUID(pehw_GUID_NULL,
            0x00000000, 0x0000, 0x0000, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00);

/* INetConnectionRefresh — how a change to a connection is announced to whoever is already
 * displaying it. netman keeps no cached copy of the per-connection settings (each
 * GetProperties re-reads them), so a value written behind its back is reported correctly on the
 * next call; what a plain write does not do is tell a component that has *already* asked. The
 * notification-area icon is built at the tray's own start-up enumeration or when this interface
 * fires, and nothing else. See README.md § The notification-area icon.
 *
 * No SDK header declares it and no import library defines the IID, so both are stated here. The
 * slot order was read out of `netman.dll` on XP SP3, Server 2003 and XP x64 — the same eight
 * methods in the same order on all three — and the signature out of the NDR format strings in
 * `netshell.dll`, which carries the marshalling for this interface: procedure 6 takes one [in]
 * parameter described as `FC_IP` with a constant IID of `IID_INetConnection`, and returns
 * HRESULT.
 *
 * That the parameter is the *connection object* and not its id is the trap here, and counting
 * arguments does not reveal it: netman's own call site pushes one pointer, and reading that as
 * "a GUID by reference" type-checks, builds, and marshals — the failure lands in netman, which
 * dereferences it as an interface and faults. The proxy hands that back as HRESULT 0xC0000005,
 * which reads like a crash in the caller and is not. So: only ConnectionModified is given a
 * signature, because it is the only one whose parameters were read from the format string; the
 * rest are named placeholders that exist to put it at the right offset. Do not call through a
 * placeholder, and do not infer a parameter's type from the number of arguments pushed. */
DEFINE_GUID(pehw_IID_INetConnectionRefresh,
            0xFAEDCF5F, 0x31FE, 0x11D1, 0xAA, 0xD2, 0x00, 0x80, 0x5F, 0xC1, 0x27, 0x0E);

typedef struct pehw_INetConnectionRefresh pehw_INetConnectionRefresh;
typedef struct pehw_INetConnectionRefreshVtbl {
    HRESULT (STDMETHODCALLTYPE *QueryInterface)(pehw_INetConnectionRefresh *, REFIID, void **);
    ULONG   (STDMETHODCALLTYPE *AddRef)(pehw_INetConnectionRefresh *);
    ULONG   (STDMETHODCALLTYPE *Release)(pehw_INetConnectionRefresh *);
    void *RefreshAll;
    void *ConnectionAdded;
    void *ConnectionDeleted;
    HRESULT (STDMETHODCALLTYPE *ConnectionModified)(pehw_INetConnectionRefresh *, INetConnection *);
    void *ConnectionRenamed;
    void *ConnectionStatusChanged;
    void *ShowBalloon;
    void *DisableEvents;
} pehw_INetConnectionRefreshVtbl;
struct pehw_INetConnectionRefresh { const pehw_INetConnectionRefreshVtbl *lpVtbl; };

/* Not in every MinGW-w64 setupapi.h, and cheap to state. */
#ifndef DI_QUIETINSTALL
#define DI_QUIETINSTALL 0x00000800
#endif
#ifndef DI_NOFILECOPY
#define DI_NOFILECOPY 0x01000000
#endif
#ifndef DIF_SELECTBESTCOMPATDRV
#define DIF_SELECTBESTCOMPATDRV 0x00000017
#endif
/* The setupapi failures worth naming. Everything here is reported as a raw code too, but these
 * three are the ones that come up every run and mean very different things. */
/* UL, not L: these are compared against GetLastError(), which is a DWORD. Signed constants make
 * every comparison a signedness mismatch. */
#define PEHW_E_NO_COMPAT_DRIVERS     0xE0000228UL
#define PEHW_E_NO_ASSOCIATED_SERVICE 0xE0000217UL
#define PEHW_E_NO_DRIVER_SELECTED    0xE0000203UL

/* The PnP manager's per-devnode status and problem code:
 *
 *     CONFIGRET WINAPI CM_Get_DevNode_Status(PULONG status, PULONG problem, DEVINST, ULONG flags);
 *
 * FOUR arguments, stdcall — the arity has to be exact, for the reason spelled out on CMPWAIT
 * below. Resolved from setupapi.dll, which re-exports the cfgmgr32 entry points on XP. */
typedef DWORD (WINAPI *CMDEVNODESTATUS)(DWORD *, DWORD *, DWORD, DWORD);

/* Problem codes worth naming; the rest are printed raw. */
#define CM_PROB_NOT_CONFIGURED     1
#define CM_PROB_FAILED_START      10
#define CM_PROB_NORMAL_CONFLICT   12
#define CM_PROB_DEVICE_NOT_THERE  24
#define CM_PROB_NEED_RESTART      14
#define CM_PROB_REINSTALL         18
#define CM_PROB_FAILED_INSTALL    28
/* The one an unconfigured NDIS miniport sits at: the driver's AddDevice returned failure. For a
 * network adapter that means NDIS refused it, which it does when the adapter has no network
 * component configuration — no NetCfgInstanceId, nothing under Control\Network. The driver is
 * bound and healthy; it is the configuration half that is missing. */
#define CM_PROB_FAILED_ADD        31
#define CM_PROB_FAILED_DRIVER_ENTRY 37
#define CM_PROB_DRIVER_FAILED_LOAD  39

static const char *problem_name(DWORD p)
{
    switch (p) {
    case 0:                          return "ok";
    case CM_PROB_NOT_CONFIGURED:     return "not configured";
    case CM_PROB_FAILED_START:       return "failed to start";
    case CM_PROB_NORMAL_CONFLICT:    return "resource conflict";
    case CM_PROB_NEED_RESTART:       return "needs a restart";
    case CM_PROB_REINSTALL:          return "needs reinstalling";
    case CM_PROB_DEVICE_NOT_THERE:   return "not present";
    case CM_PROB_FAILED_INSTALL:     return "no driver installed";
    case CM_PROB_FAILED_ADD:         return "AddDevice failed (a NIC here means it is unconfigured)";
    case CM_PROB_FAILED_DRIVER_ENTRY:return "DriverEntry failed";
    case CM_PROB_DRIVER_FAILED_LOAD: return "driver failed to load";
    default:                         return "";
    }
}

/* setupapi's private "are the PnP services up yet?" wait, exported by name and undocumented:
 *
 *     CONFIGRET WINAPI CMP_WaitServicesAvailable(HMACHINE hMachine);
 *
 * ONE argument, and it is stdcall — so the arity has to be exactly right. Declaring a second
 * parameter makes the caller push 8 bytes for a callee that pops 4, and the 4-byte imbalance
 * does not fault here: execution carries on with a corrupted frame and dies later at a
 * nonsense address, which looks like a crash inside whatever ran next. */
typedef DWORD (WINAPI *CMPWAIT)(HANDLE);

/* A readable name for the codes that recur, so a log does not need a lookup table to read. */
static const char *spapi_name(DWORD e)
{
    switch (e) {
    case PEHW_E_NO_COMPAT_DRIVERS:     return "no compatible driver in the INF path";
    case PEHW_E_NO_ASSOCIATED_SERVICE: return "the INF declares no service for it";
    case PEHW_E_NO_DRIVER_SELECTED:    return "no driver selected";
    case ERROR_ACCESS_DENIED:           return "access denied";
    case ERROR_INVALID_DATA:            return "invalid data";
    default:                            return "";
    }
}

static CMDEVNODESTATUS g_devnode_status;   /* resolved once; NULL if setupapi has no such export */
static int g_verbose;                      /* -v: the per-device trace, off on the boot path */

/* Where the time goes. The pass costs a second by hand and two minutes at boot, and the two
 * runs differ in what they skip, so the cost has to be attributed rather than guessed at.
 * Milliseconds, accumulated across every device. */
static DWORD g_ms_search;    /* building the driver list and choosing from it */
static DWORD g_ms_install;   /* DIF_INSTALLDEVICE */
static DWORD g_ms_restart;   /* DIF_PROPERTYCHANGE, the devnode restart */
static DWORD g_ms_probe;     /* deciding whether a device needs anything at all */

/* Unsigned subtraction, so the GetTickCount wrap is still a correct elapsed time. */
#define SINCE(t0) (GetTickCount() - (t0))

/* Detail that is worth reading when you run this by hand and is noise when it scrolls past at
 * boot. Writing to a VGA console is genuinely slow, so this is a speed decision as much as a
 * readability one. */
static void say_v(const char *fmt, ...);

/* What the pass is doing right now, so an unhandled exception can name it. A crash inside
 * setupapi or newdev otherwise leaves no trace at all: the process dies, the runner records
 * the exit code, and nothing says which device or which call was in flight. */
static char g_where[512];

/* Bounded, because what gets formatted in here is not under this file's control: a device
 * description comes from the registry and is itself up to 512 bytes, and a component id comes
 * from the command line. wsprintfA takes no size and stops only at its own 1024-character
 * limit, so either would write past the buffer — and this is the crash-reporting buffer, whose
 * whole job is to still be readable when something has gone wrong. */
static void where(const char *fmt, ...)
{
    va_list ap;

    va_start(ap, fmt);
    _vsnprintf(g_where, sizeof g_where - 1, fmt, ap);
    va_end(ap);
    /* _vsnprintf leaves the buffer unterminated when the text was truncated. */
    g_where[sizeof g_where - 1] = 0;
}

#define WHERE(...) where(__VA_ARGS__)

/* cppcheck-suppress constParameterCallback ; must match LPTOP_LEVEL_EXCEPTION_FILTER exactly */
static LONG WINAPI crash_filter(EXCEPTION_POINTERS *ep)
{
    printf("CRASH exception 0x%08lX at %p while %s\n",
           (unsigned long)ep->ExceptionRecord->ExceptionCode,
           ep->ExceptionRecord->ExceptionAddress, g_where);
    fflush(stdout);
    return EXCEPTION_EXECUTE_HANDLER;
}

/* Progress goes to stdout: the run-list runner captures it and keeps the last line as the
 * task's Message, so this doubles as the status a hive dump shows. */
static void say(const char *fmt, ...)
{
    va_list ap;

    va_start(ap, fmt);
    vprintf(fmt, ap);
    va_end(ap);
    printf("\n");
    fflush(stdout);
}

/* Enable one privilege the token already holds. Holding a privilege and having it *enabled* are
 * different things: a token carries most of them disabled, and installing a driver needs
 * SeLoadDriverPrivilege actually on. Without it the install returns ERROR_ACCESS_DENIED even
 * as SYSTEM, which reads like a permissions problem no PE could ever have. */
static int enable_priv(const char *name)
{
    HANDLE tok;
    TOKEN_PRIVILEGES tp;
    int ok = 0;

    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_ADJUST_PRIVILEGES | TOKEN_QUERY, &tok))
        return 0;
    ZeroMemory(&tp, sizeof tp);
    tp.PrivilegeCount = 1;
    tp.Privileges[0].Attributes = SE_PRIVILEGE_ENABLED;
    if (LookupPrivilegeValueA(NULL, name, &tp.Privileges[0].Luid)) {
        AdjustTokenPrivileges(tok, FALSE, &tp, 0, NULL, NULL);
        /* AdjustTokenPrivileges reports success even when it enabled nothing, so the real
         * verdict is in GetLastError. */
        ok = (GetLastError() == ERROR_SUCCESS);
    }
    CloseHandle(tok);
    return ok;
}

static void enable_install_privs(void)
{
    static const char *names[] = {
        "SeLoadDriverPrivilege",      /* the one the install actually needs */
        "SeRestorePrivilege",         /* setupapi writes protected locations */
        "SeBackupPrivilege",
        "SeTakeOwnershipPrivilege",
        "SeSecurityPrivilege",
        NULL
    };
    int i;

    /* Only the failures are worth a line. Five "enabled" messages on every boot say nothing a
     * reader can act on, while a privilege that would not enable explains every ACCESS_DENIED
     * that follows — so silence here means all is well, and any output is a real finding. */
    for (i = 0; names[i]; i++)
        if (enable_priv(names[i]))
            say_v("privilege %s: enabled", names[i]);
        else
            say("privilege %s: NOT enabled - installs will fail with access denied", names[i]);
}

static void say_v(const char *fmt, ...)
{
    va_list ap;

    if (!g_verbose)
        return;
    va_start(ap, fmt);
    vprintf(fmt, ap);
    va_end(ap);
    printf("\n");
    fflush(stdout);
}

static void resolve_cm(void)
{
    HMODULE sa = GetModuleHandleA("setupapi.dll");

    g_devnode_status = sa ? (CMDEVNODESTATUS)(void(*)(void))
        GetProcAddress(sa, "CM_Get_DevNode_Status") : NULL;
}

/* Whether this device is worth touching. A device that already has a driver bound and reports no
 * PnP problem is working; reinstalling it is pure churn — seconds each, on the boot path, for a
 * device that was fine. It also makes the pass idempotent, so a re-run costs almost nothing.
 *
 * Erring towards installing is deliberate: if the problem code cannot be read, treat the device
 * as needing work rather than silently skipping something broken. */
static int needs_install(HDEVINFO set, SP_DEVINFO_DATA *dev)
{
    char svc[256];
    DWORD status = 0, problem = 0;

    svc[0] = 0;
    SetupDiGetDeviceRegistryPropertyA(set, dev, SPDRP_SERVICE, NULL,
                                      (PBYTE)svc, sizeof svc, NULL);
    if (!svc[0])
        return 1;                      /* nothing bound — precisely what this pass is for */
    if (!g_devnode_status)
        return 1;
    if (g_devnode_status(&status, &problem, dev->DevInst, 0) != 0)
        return 1;                      /* could not tell; assume it needs help */
    return problem != 0;
}

/* Block until the PnP service stack answers. A device install issued before it is up fails in
 * ways that look like a missing driver — one of the MiniNT quirks the OPK tools revealed. */
static void wait_pnp(void)
{
    HMODULE m = GetModuleHandleA("setupapi.dll");
    CMPWAIT wait = m ? (CMPWAIT)(void(*)(void))GetProcAddress(m, "CMP_WaitServicesAvailable") : NULL;

    if (wait)
        wait(NULL);   /* NULL = this machine */
}

/* Install the best compatible driver for one device, the way a real setup pass does: let
 * setupapi choose from the INF search path rather than naming an INF ourselves, then hand the
 * chosen INF back to newdev. Returns 1 if the device was installed. */
static int install_one(HDEVINFO set, SP_DEVINFO_DATA *dev, const char *desc)
{
    SP_DEVINSTALL_PARAMS_A parm;
    SP_DRVINFO_DATA_V2_A drv;
    SP_DRVINFO_DETAIL_DATA_A *detail;
    char hwid[0x1000], full[MAX_PATH];
    BYTE buf[2048];
    DWORD t;
    int ok = 0;

    /* Suppress every prompt before anything else: a PE has nobody to answer a dialog, and a
     * blocked install would sit there until the task's timeout killed it. */
    /* Read and show the hardware ID before anything else. Nothing has a friendly name until it
     * has a driver, so pre-install this is the only thing that identifies a device — and it is
     * what has to match an INF for any of the rest to happen. */
    WHERE("reading the hardware id for %s", desc);
    hwid[0] = 0;
    SetupDiGetDeviceRegistryPropertyA(set, dev, SPDRP_HARDWAREID, NULL,
                                      (PBYTE)hwid, sizeof hwid, NULL);
    say_v("    hwid %s", hwid[0] ? hwid : "(none)");
    if (!hwid[0])
        return 0;

    WHERE("setting quiet install params on %s", desc);
    ZeroMemory(&parm, sizeof parm);
    parm.cbSize = sizeof parm;
    if (SetupDiGetDeviceInstallParamsA(set, dev, &parm)) {
        /* DI_NOFILECOPY is what makes an install possible at all on boot media: the install
         * otherwise runs the INF's CopyFiles into %windir%\system32\drivers, which is on the
         * read-only CD, and the refusal surfaces as a bare ERROR_ACCESS_DENIED identical for
         * every device. It is the direct-call equivalent of newdev's INSTALLFLAG_READONLY.
         *
         * The corollary is that the driver binary has to be staged already — see
         * README.md § Which INFs are staged. An INF without its .sys installs cleanly here and
         * then fails to start, which is a far more confusing failure than this one. */
        parm.Flags |= DI_QUIETINSTALL | DI_NOFILECOPY;
        SetupDiSetDeviceInstallParamsA(set, dev, &parm);
    }

    t = GetTickCount();
    WHERE("building the driver list for %s", desc);
    if (!SetupDiBuildDriverInfoList(set, dev, SPDIT_COMPATDRIVER)) {
        g_ms_search += SINCE(t);
        say_v("    no driver list (error 0x%08lX)", (unsigned long)GetLastError());
        return 0;
    }
    /* Count the candidates the search turned up, before asking for a choice between them. This
     * is what separates "no staged INF claims this device" (0 candidates — an INF-side problem)
     * from "the match worked and the selection step failed" (candidates listed, selection still
     * fails — a class-installer problem). The two are identical from outside, and the fix for
     * one does nothing for the other. */
    {
        SP_DRVINFO_DATA_V2_A cand;
        DWORD ci;
        int found = 0;

        for (ci = 0;; ci++) {
            ZeroMemory(&cand, sizeof cand);
            cand.cbSize = sizeof cand;
            if (!SetupDiEnumDriverInfoA(set, dev, SPDIT_COMPATDRIVER, ci,
                                        (SP_DRVINFO_DATA_A *)&cand))
                break;
            if (found < 3)
                say_v("    candidate: %s [%s]", cand.Description, cand.MfgName);
            found++;
        }
        say_v("    %d candidate driver(s)", found);
    }

    WHERE("selecting the best driver for %s", desc);
    if (!SetupDiCallClassInstaller(DIF_SELECTBESTCOMPATDRV, set, dev)) {
        /* Usually ERROR_NO_COMPAT_DRIVERS (0xE0000228) — no INF we staged claims this device,
         * which is the expected answer for most of them. Reported rather than skipped in
         * silence: a device we *did* stage an INF for landing here is the interesting case, and
         * it is indistinguishable from the boring one otherwise. */
        g_ms_search += SINCE(t);
        say_v("    no driver selected (error 0x%08lX)", (unsigned long)GetLastError());
        SetupDiDestroyDriverInfoList(set, dev, SPDIT_COMPATDRIVER);
        return 0;
    }
    g_ms_search += SINCE(t);

    WHERE("reading the selected driver for %s", desc);
    ZeroMemory(&drv, sizeof drv);
    drv.cbSize = sizeof drv;
    if (!SetupDiGetSelectedDriverA(set, dev, (SP_DRVINFO_DATA_A *)&drv))
        goto done;

    WHERE("reading driver detail for %s", desc);
    detail = (SP_DRVINFO_DETAIL_DATA_A *)buf;
    ZeroMemory(detail, sizeof *detail);
    detail->cbSize = sizeof *detail;
    if (!SetupDiGetDriverInfoDetailA(set, dev, (SP_DRVINFO_DATA_A *)&drv, detail, sizeof buf, NULL)
        && GetLastError() != ERROR_INSUFFICIENT_BUFFER)
        goto done;
    if (!detail->InfFileName[0])
        goto done;
    if (!GetFullPathNameA(detail->InfFileName, sizeof full, full, NULL))
        goto done;

    /* Printed before the install, not after: this is the call that can hang or fault, and if it
     * does this line is the last thing on screen — which is the whole diagnostic. */
    say("installing %s <- %s [%s]", desc, full, hwid);
    WHERE("installing %s", desc);
    t = GetTickCount();

    /* Install the driver already selected above, through setupapi directly.
     *
     * NOT through newdev's UpdateDriverForPlugAndPlayDevices, which is what a first version used
     * because BartPE's tool did: that wrapper *installs the INF* as part of its job — the
     * SetupCopyOEMInf-style write into %windir%\inf that the OPK tools have to skip in WinPE.
     * %windir% here is x:\i386 on a read-only CD, so that write is ERROR_ACCESS_DENIED and every
     * device fails identically, whatever its driver. DIF_INSTALLDEVICE binds the driver that is
     * already chosen and does not re-install the INF it came from. */
    (void)full;
    if (SetupDiCallClassInstaller(DIF_INSTALLDEVICE, set, dev)) {
        SP_PROPCHANGE_PARAMS pcp;

        g_ms_install += SINCE(t);
        t = GetTickCount();

        /* Installing a driver onto an already-enumerated devnode does not start it. The device
         * keeps running whatever it had (nothing, here), so the new driver sits registered and
         * unloaded and PnP reports problem 31 — while starting the service by hand works fine,
         * which is a confusing pair of symptoms. newdev's wrapper restarted the devnodes it
         * touched; calling DIF_INSTALLDEVICE directly means doing it here.
         *
         * DICS_PROPCHANGE is the "your configuration changed, restart yourself" signal, and is
         * what the class installer turns into a stop/start of the devnode. */
        ZeroMemory(&pcp, sizeof pcp);
        pcp.ClassInstallHeader.cbSize = sizeof(SP_CLASSINSTALL_HEADER);
        pcp.ClassInstallHeader.InstallFunction = DIF_PROPERTYCHANGE;
        pcp.StateChange = DICS_PROPCHANGE;
        pcp.Scope = DICS_FLAG_CONFIGSPECIFIC;
        pcp.HwProfile = 0;

        WHERE("restarting the devnode for %s", desc);
        if (SetupDiSetClassInstallParamsA(set, dev, &pcp.ClassInstallHeader, sizeof pcp)
            && SetupDiCallClassInstaller(DIF_PROPERTYCHANGE, set, dev))
            say("installed %s (restarted)", desc);
        else
            say("installed %s (restart failed, error 0x%08lX - may need a reboot to start)",
                desc, (unsigned long)GetLastError());
        g_ms_restart += SINCE(t);
        ok = 1;
    } else {
        DWORD e = GetLastError();

        g_ms_install += SINCE(t);

        /* A device whose INF section declares no service is not a failure — plenty of buses and
         * bridges need no driver at all, and the install has still done its part. Reported
         * separately so it stops looking like something to fix. */
        if (e == PEHW_E_NO_ASSOCIATED_SERVICE)
            say_v("    no service to start (nothing to install)");
        else
            say("failed %s (error 0x%08lX %s)", desc, (unsigned long)e, spapi_name(e));
    }

done:
    SetupDiDestroyDriverInfoList(set, dev, SPDIT_COMPATDRIVER);
    return ok;
}

/* Best-effort friendly name for a device, for the log. Falls back to the instance id. */
static void describe(HDEVINFO set, SP_DEVINFO_DATA *dev, char *out, DWORD cch)
{
    /* Device names come out of the registry in the ANSI code page (1251 on Russian media) while
     * the console renders the OEM one (866), so a localized name prints as mojibake unless it is
     * converted. CharToOemA does exactly that mapping. Instance ids are ASCII and unaffected,
     * but converting them costs nothing and keeps one exit path. */
    if (SetupDiGetDeviceRegistryPropertyA(set, dev, SPDRP_FRIENDLYNAME, NULL,
                                          (PBYTE)out, cch, NULL) && out[0])
        goto oem;
    if (SetupDiGetDeviceRegistryPropertyA(set, dev, SPDRP_DEVICEDESC, NULL,
                                          (PBYTE)out, cch, NULL) && out[0])
        goto oem;
    if (!SetupDiGetDeviceInstanceIdA(set, dev, out, cch, NULL))
        lstrcpynA(out, "(unnamed device)", (int)cch);
oem:
    CharToOemA(out, out);
}

/* Report the INF search path as setupapi will see it, and whether anything is actually there.
 * DIF_SELECTBESTCOMPATDRV searches this path and says only "no compatible driver" when it is
 * wrong or empty — the same answer it gives for a device we never intended to support, so the
 * two are indistinguishable without checking directly. */
static void report_inf_path(void)
{
    HKEY k;
    char raw[MAX_PATH * 4], path[MAX_PATH * 4], glob[MAX_PATH * 4];
    DWORD type = 0, cb = sizeof raw;
    WIN32_FIND_DATAA fd;
    HANDLE h;
    int n = 0;

    raw[0] = 0;
    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, "SOFTWARE\\Microsoft\\Windows\\CurrentVersion",
                      0, KEY_READ, &k) == ERROR_SUCCESS) {
        if (RegQueryValueExA(k, "DevicePath", NULL, &type, (BYTE *)raw, &cb) != ERROR_SUCCESS)
            raw[0] = 0;
        RegCloseKey(k);
    }
    if (!raw[0]) {
        say("DevicePath is not set - setupapi has no INF search path");
        return;
    }
    /* ExpandEnvironmentStrings reports the size it *needed*, and writes nothing when that
     * exceeds the buffer — so a zero return is not the only failure. Taking the unexpanded
     * text is a poor search path but a defined one; leaving `path` uninitialized is not. */
    {
        DWORD need = ExpandEnvironmentStringsA(raw, path, sizeof path);

        if (need == 0 || need > sizeof path)
            lstrcpynA(path, raw, sizeof path);
    }

    /* DevicePath may list several directories; only the first is inspected here, which is all
     * this build sets. */
    _snprintf(glob, sizeof glob - 1, "%s\\*.inf", path);
    glob[sizeof glob - 1] = 0;
    h = FindFirstFileA(glob, &fd);
    if (h != INVALID_HANDLE_VALUE) {
        do { n++; } while (FindNextFileA(h, &fd));
        FindClose(h);
    }
    /* The happy case is a boot-path line nobody reads; an empty search path is the thing that
     * makes every device report "no driver node", so that one is said out loud. */
    if (n)
        say_v("DevicePath %s -> %s (%d .inf files readable)", raw, path, n);
    else
        say("DevicePath %s -> %s has NO readable .inf files", raw, path);

    /* Whether that directory is writable, stated outright. setupapi wants to write beside an
     * INF (a .PNF, and the INF-install copy) and reports the refusal as a plain
     * ERROR_ACCESS_DENIED on the install — which reads like a privilege problem rather than a
     * read-only volume, and sends you looking in the wrong place. */
    {
        char probe[MAX_PATH * 4];
        HANDLE t;

        _snprintf(probe, sizeof probe - 1, "%s\\pehw_wt.tmp", path);
        probe[sizeof probe - 1] = 0;
        t = CreateFileA(probe, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS,
                        FILE_ATTRIBUTE_TEMPORARY | FILE_FLAG_DELETE_ON_CLOSE, NULL);
        if (t == INVALID_HANDLE_VALUE)
            say_v("  the INF directory is NOT writable (error %lu) - expected on boot media",
                  (unsigned long)GetLastError());
        else {
            CloseHandle(t);
            say_v("  the INF directory is writable");
        }
    }
}

static int register_netcfgx(void);

static int cmd_devices(int force)
{
    HDEVINFO set;
    SP_DEVINFO_DATA dev;
    DWORD i, t, t_all, took, ms_wait = 0, worst_ms = 0;
    char worst[256];
    int seen = 0, installed = 0, healthy = 0;

    worst[0] = 0;

    t_all = GetTickCount();
    SetUnhandledExceptionFilter(crash_filter);
    resolve_cm();

    WHERE("enabling install privileges");
    enable_install_privs();

    /* Register netcfgx.dll before installing anything, not just before a component install.
     * Installing a network adapter runs netcfgx's own Net class installer, and that class
     * installer turns the adapter into an INetCfg component — which it cannot do while
     * CLSID_CNetCfg is unregistered, as it is in a PE until somebody registers it. The adapter
     * then installs with a bound, healthy driver and no network configuration, and NDIS fails
     * its AddDevice: PnP problem 31, a device that will not start for reasons nothing about the
     * driver explains. Cheap and idempotent, so it costs a working image nothing.
     * See README.md § Registering the COM server. */
    register_netcfgx();
    report_inf_path();

    WHERE("waiting for the PnP services (CMP_WaitServicesAvailable)");
    t = GetTickCount();
    wait_pnp();
    ms_wait = SINCE(t);

    /* Every device the PnP manager currently reports, which is what a real install pass walks.
     * DIGCF_PRESENT keeps it to hardware actually here rather than phantom devnodes. */
    WHERE("enumerating the device tree");
    set = SetupDiGetClassDevsA(NULL, NULL, NULL, DIGCF_ALLCLASSES | DIGCF_PRESENT);
    if (set == INVALID_HANDLE_VALUE) {
        say("cannot enumerate devices (error %lu)", (unsigned long)GetLastError());
        return 1;
    }

    for (i = 0;; i++) {
        char desc[512];

        WHERE("enumerating device %lu", (unsigned long)i);
        ZeroMemory(&dev, sizeof dev);
        dev.cbSize = sizeof dev;
        if (!SetupDiEnumDeviceInfo(set, i, &dev))
            break;
        seen++;

        /* Leave working devices alone. This is what keeps the pass off the boot path's critical
         * time: most of a QEMU device tree is chipset and bus devices that the boot driver set
         * already handles, and reinstalling those was the bulk of the runtime. */
        t = GetTickCount();
        if (!force && !needs_install(set, &dev)) {
            g_ms_probe += SINCE(t);
            healthy++;
            continue;
        }
        g_ms_probe += SINCE(t);

        WHERE("describing device %lu", (unsigned long)i);
        describe(set, &dev, desc, sizeof desc);
        /* Announced as it is reached, so the console shows how far the pass got if it dies
         * partway — but only in the detail the caller asked for. */
        say_v("[%lu] %s", (unsigned long)i, desc);

        t = GetTickCount();
        installed += install_one(set, &dev, desc);
        took = SINCE(t);
        if (took >= 1000)
            say("  (%s took %lu ms)", desc, (unsigned long)took);
        /* Remember the worst one. At boot every line but the last is swallowed by the runner's
         * pipe, so anything that must survive has to reach the summary — and "one device ate
         * the whole budget" is a completely different problem from "thirty each cost a bit". */
        if (took > worst_ms) {
            worst_ms = took;
            lstrcpynA(worst, desc, sizeof worst);
        }
    }
    SetupDiDestroyDeviceInfoList(set);

    /* Where the time went, always — this is on the boot path and the total is the thing people
     * complain about, so it should never need a special build to explain itself. */
    say("%d device(s) present, %d already configured, %d installed", seen, healthy, installed);
    /* One self-contained line, because it is the only one that survives the runner's pipe: the
     * counts, the breakdown, and which single device was worst. */
    say("%d present/%d configured/%d installed; %lu ms = %lu wait + %lu search + %lu install"
        " + %lu restart; slowest %s %lu ms",
        seen, healthy, installed,
        (unsigned long)SINCE(t_all), (unsigned long)ms_wait, (unsigned long)g_ms_search,
        (unsigned long)g_ms_install, (unsigned long)g_ms_restart,
        worst[0] ? worst : "(none)", (unsigned long)worst_ms);
    return 0;
}

/* The state of one driver service, as the SCM sees it. A device can be installed — driver bound,
 * service key written — and still have nothing running, which is precisely the difference
 * between a real install and the registry state a build used to bake. */
static const char *service_state(const char *name)
{
    SC_HANDLE scm, svc;
    SERVICE_STATUS st;
    const char *s = "unknown";

    if (!name || !name[0])
        return "(none)";
    scm = OpenSCManagerA(NULL, NULL, SC_MANAGER_CONNECT);
    if (!scm)
        return "SCM unreachable";
    svc = OpenServiceA(scm, name, SERVICE_QUERY_STATUS);
    if (!svc) {
        CloseServiceHandle(scm);
        return GetLastError() == ERROR_SERVICE_DOES_NOT_EXIST ? "NOT REGISTERED" : "unopenable";
    }
    if (QueryServiceStatus(svc, &st)) {
        switch (st.dwCurrentState) {
        case SERVICE_STOPPED:          s = "stopped"; break;
        case SERVICE_START_PENDING:    s = "starting"; break;
        case SERVICE_STOP_PENDING:     s = "stopping"; break;
        case SERVICE_RUNNING:          s = "RUNNING"; break;
        case SERVICE_CONTINUE_PENDING: s = "continuing"; break;
        case SERVICE_PAUSE_PENDING:    s = "pausing"; break;
        case SERVICE_PAUSED:           s = "paused"; break;
        default:                       s = "?"; break;
        }
    }
    CloseServiceHandle(svc);
    CloseServiceHandle(scm);
    return s;
}

/* Report what the device tree looks like *after* an install pass: which devices have a driver
 * bound, what service each one runs, whether that service is actually running, and the PnP
 * problem code for the ones that are not. Read-only — it changes nothing.
 *
 * This exists because a PE has nothing else to ask with: no Device Manager, no sc.exe, no
 * reg.exe. Without it the install pass reports success and leaves no way to tell whether the
 * driver ever loaded, which is exactly the distinction that matters here. */
static int cmd_status(void)
{
    HDEVINFO set;
    SP_DEVINFO_DATA dev;
    DWORD i;
    int seen = 0, bound = 0, running = 0;

    resolve_cm();

    set = SetupDiGetClassDevsA(NULL, NULL, NULL, DIGCF_ALLCLASSES | DIGCF_PRESENT);
    if (set == INVALID_HANDLE_VALUE) {
        say("cannot enumerate devices (error %lu)", (unsigned long)GetLastError());
        return 1;
    }

    for (i = 0;; i++) {
        char desc[512], svc[256], hwid[512];
        DWORD status = 0, problem = 0;

        ZeroMemory(&dev, sizeof dev);
        dev.cbSize = sizeof dev;
        if (!SetupDiEnumDeviceInfo(set, i, &dev))
            break;
        seen++;

        describe(set, &dev, desc, sizeof desc);
        svc[0] = 0;
        SetupDiGetDeviceRegistryPropertyA(set, &dev, SPDRP_SERVICE, NULL,
                                          (PBYTE)svc, sizeof svc, NULL);
        hwid[0] = 0;
        SetupDiGetDeviceRegistryPropertyA(set, &dev, SPDRP_HARDWAREID, NULL,
                                          (PBYTE)hwid, sizeof hwid, NULL);
        if (g_devnode_status)
            g_devnode_status(&status, &problem, dev.DevInst, 0);

        if (svc[0]) {
            const char *st = service_state(svc);

            bound++;
            if (lstrcmpA(st, "RUNNING") == 0)
                running++;
            say("[%lu] %s", (unsigned long)i, desc);
            say("    hwid %s", hwid[0] ? hwid : "(none)");
            say("    service %s = %s   problem %lu %s",
                svc, st, (unsigned long)problem, problem_name(problem));
        } else {
            say("[%lu] %s", (unsigned long)i, desc);
            say("    hwid %s", hwid[0] ? hwid : "(none)");
            say("    no driver bound          problem %lu %s",
                (unsigned long)problem, problem_name(problem));
        }
    }
    SetupDiDestroyDeviceInfoList(set);

    say("%d device(s), %d with a driver bound, %d with the service running", seen, bound, running);
    return 0;
}

/* Resolve a service's ImagePath the way the kernel does, so it can be checked for existence.
 * Driver paths are almost always "\SystemRoot\System32\drivers\x.sys" or a bare relative
 * "system32\drivers\x.sys" — neither of which opens as written. */
static int lstrchrA_has_sep(const char *s)
{
    for (; *s; s++)
        if (*s == '\\' || *s == '/')
            return 1;
    return 0;
}

/* Whether the last path component carries an extension — a directory further up having a dot
 * says nothing about the file. */
static int lstrchrA_has_dot_in_tail(const char *s)
{
    const char *dot = NULL;

    for (; *s; s++)
        if (*s == '.')
            dot = s;
        else if (*s == '\\' || *s == '/')
            dot = NULL;
    return dot != NULL;
}

/* Where a bare name — one with no directory in it at all — is looked for. It is the only thing
 * that differs between resolving a driver's ImagePath and a service's ServiceDll, and getting it
 * wrong is not a near miss: it reports a file that is present as MISSING, under a path nothing
 * ever used. */
#define BARE_IN_DRIVERS  1  /* ImagePath=ndis.sys        -> system32\drivers\ndis.sys */
#define BARE_IN_SYSTEM32 0  /* ServiceDll=dhcpcsvc.dll   -> system32\dhcpcsvc.dll */

static void resolve_image(const char *img, char *out, DWORD cch, int bare)
{
    char win[MAX_PATH], work[MAX_PATH * 2];
    char *p;

    if (!GetWindowsDirectoryA(win, sizeof win))
        win[0] = 0;

    /* A win32 service's ImagePath is a command line, not a path: it can carry %SystemRoot% and
     * arguments ("%SystemRoot%\system32\svchost.exe -k netsvcs"). Checked literally, a perfectly
     * healthy svchost service reports its binary MISSING — a diagnostic stating the opposite of
     * the truth, which is worse than not checking. Expand the variables and cut the arguments
     * before deciding anything. */
    {
        /* A return larger than the buffer means it wrote nothing — see report_inf_path. */
        DWORD need = ExpandEnvironmentStringsA(img, work, sizeof work);

        if (need == 0 || need > sizeof work)
            lstrcpynA(work, img, sizeof work);
    }
    /* A quoted image ends at its closing quote; an unquoted one ends where an argument starts. */
    if (work[0] == '"') {
        MoveMemory(work, work + 1, lstrlenA(work));
        for (p = work; *p; p++)
            if (*p == '"') {
                *p = 0;
                break;
            }
    } else {
        for (p = work; *p; p++)
            if (p[0] == ' ' && (p[1] == '-' || p[1] == '/')) {
                *p = 0;
                break;
            }
    }
    img = work;

    /* Every branch below is bounded by cch. The caller's buffer is not necessarily larger than
     * %windir% plus an ImagePath read from the registry, and an ImagePath is whatever the hive
     * happens to contain. */
    if (lstrlenA(img) > 12 && CompareStringA(LOCALE_INVARIANT, NORM_IGNORECASE,
                                             img, 12, "\\SystemRoot\\", 12) == CSTR_EQUAL)
        _snprintf(out, cch - 1, "%s\\%s", win, img + 12);
    else if (img[0] == '\\' || (img[1] == ':'))
        lstrcpynA(out, img, (int)cch);          /* already absolute, or an NT path we cannot fix */
    else if (!lstrchrA_has_sep(img))
        /* A bare file name, which is how the in-box drivers are registered (ImagePath=ndis.sys).
         * Neither kind is looked for in %windir% itself — resolving them the same way as a path
         * with directories in it reports a loaded driver as MISSING. Which of the two
         * directories applies is the caller's to say; see BARE_IN_DRIVERS. */
        _snprintf(out, cch - 1, "%s\\system32\\%s%s", win,
                  bare == BARE_IN_DRIVERS ? "drivers\\" : "", img);
    else
        _snprintf(out, cch - 1, "%s\\%s", win, img);  /* a relative path, from %windir% */
    out[cch - 1] = 0;

    /* An ImagePath may omit the extension — RpcSs is registered as `svchost -k rpcss` on both
     * media, and the loader supplies the .exe. Checked literally, the healthiest service in the
     * image reports its binary MISSING. Only append when the path as written is not there, so a
     * genuinely absent file still reads as absent. */
    if (GetFileAttributesA(out) == INVALID_FILE_ATTRIBUTES && !lstrchrA_has_dot_in_tail(out)) {
        char withext[MAX_PATH * 2];

        _snprintf(withext, sizeof withext - 1, "%s.exe", out);
        withext[sizeof withext - 1] = 0;
        if (GetFileAttributesA(withext) != INVALID_FILE_ATTRIBUTES)
            lstrcpynA(out, withext, (int)cch);
    }
}

/* Report a driver service the way `sc qc` would, for an image that has no sc.exe: what the SCM
 * thinks, what the registry says, and — the one that actually catches mistakes — whether the
 * binary is where ImagePath claims. A service can be registered perfectly and point at a file
 * the build never staged. */
static int cmd_service(int argc, char **argv)
{
    int i;

    for (i = 2; i < argc; i++) {
        char key[512], img[MAX_PATH * 2], grp[128], obj[256], dll[MAX_PATH * 2];
        DWORD type = 0, cb, start = 0xFFFFFFFF, styp = 0xFFFFFFFF;
        HKEY k;

        say("%s = %s", argv[i], service_state(argv[i]));

        /* _snprintf, not wsprintfA: the name comes from the command line and wsprintfA bounds
         * its own output at 1K rather than the destination. */
        _snprintf(key, sizeof key - 1, "SYSTEM\\CurrentControlSet\\Services\\%s", argv[i]);
        key[sizeof key - 1] = 0;
        if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, key, 0, KEY_READ, &k) != ERROR_SUCCESS) {
            say("    no service key under CurrentControlSet\\Services");
            continue;
        }
        cb = sizeof start; RegQueryValueExA(k, "Start", NULL, &type, (BYTE *)&start, &cb);
        cb = sizeof styp;  RegQueryValueExA(k, "Type",  NULL, &type, (BYTE *)&styp,  &cb);
        grp[0] = 0; cb = sizeof grp; RegQueryValueExA(k, "Group", NULL, &type, (BYTE *)grp, &cb);
        img[0] = 0; cb = sizeof img; RegQueryValueExA(k, "ImagePath", NULL, &type, (BYTE *)img, &cb);
        /* The account, which decides what a win32 service can read once it is running — a
         * service reads its own configuration under it, so a key it has no access to stops the
         * host before it reports ready. NetworkService and LocalService are both in use here,
         * named by the media's own INF rather than by anything this chooses. A service key with
         * no ObjectName means LocalSystem; reporting "(none)" without saying so would read as a
         * missing value rather than the default it is. */
        obj[0] = 0; cb = sizeof obj; RegQueryValueExA(k, "ObjectName", NULL, &type, (BYTE *)obj, &cb);
        RegCloseKey(k);

        /* A shared-process service runs no binary of its own: svchost loads its ServiceDll, and
         * that is the file whose absence makes the host exit rather than report ready. */
        dll[0] = 0;
        _snprintf(key, sizeof key - 1, "SYSTEM\\CurrentControlSet\\Services\\%s\\Parameters", argv[i]);
        key[sizeof key - 1] = 0;
        if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, key, 0, KEY_READ, &k) == ERROR_SUCCESS) {
            cb = sizeof dll;
            RegQueryValueExA(k, "ServiceDll", NULL, &type, (BYTE *)dll, &cb);
            RegCloseKey(k);
        }

        say("    Start=%lu Type=%lu Group=%s",
            (unsigned long)start, (unsigned long)styp, grp[0] ? grp : "(none)");
        say("    ObjectName=%s", obj[0] ? obj : "(unset - LocalSystem)");
        if (dll[0]) {
            char full[MAX_PATH * 2];

            resolve_image(dll, full, sizeof full, BARE_IN_SYSTEM32);
            say("    ServiceDll=%s", dll);
            say("      -> %s (%s)", full,
                GetFileAttributesA(full) != INVALID_FILE_ATTRIBUTES ? "present" : "MISSING");
        } else if (styp & SERVICE_WIN32_SHARE_PROCESS) {
            /* Say so rather than printing nothing. This service runs inside svchost, so a
             * ServiceDll is what it would run; absent, there is nothing for the host to load,
             * and silence here is indistinguishable from an ordinary driver service. */
            say("    ServiceDll=(none) - a shared-process service with nothing to load");
        }
        {
            char full[MAX_PATH * 2];

            if (img[0]) {
                resolve_image(img, full, sizeof full, BARE_IN_DRIVERS);
                say("    ImagePath=%s", img);
            } else {
                char derived[MAX_PATH * 2];

                /* No ImagePath is normal and not a fault: the kernel derives
                 * system32\drivers\<service>.sys from the service name, which is how the
                 * in-box drivers are registered. Say which file that means, so "no ImagePath"
                 * does not read as "nothing to load". */
                _snprintf(derived, sizeof derived - 1, "system32\\drivers\\%s.sys", argv[i]);
                derived[sizeof derived - 1] = 0;
                resolve_image(derived, full, sizeof full, BARE_IN_DRIVERS);
                say("    no ImagePath - the kernel derives it from the service name");
            }
            say("      -> %s (%s)", full,
                GetFileAttributesA(full) != INVALID_FILE_ATTRIBUTES ? "present" : "MISSING");
        }
    }
    return 0;
}

/* Try to start a driver service and say exactly what happened. A PnP miniport is demand-start:
 * the PnP manager is supposed to start it when it starts the device, and when that silently does
 * not happen there is no way to tell a driver that *cannot* load from one that was never asked
 * to. Asking directly separates the two, and the failure code says which. */
static int cmd_start(int argc, char **argv)
{
    SC_HANDLE scm;
    int i, bad = 0;

    scm = OpenSCManagerA(NULL, NULL, SC_MANAGER_CONNECT);
    if (!scm) {
        say("cannot reach the SCM (error %lu)", (unsigned long)GetLastError());
        return 1;
    }
    for (i = 2; i < argc; i++) {
        SC_HANDLE svc = OpenServiceA(scm, argv[i], SERVICE_START | SERVICE_QUERY_STATUS);

        if (!svc) {
            say("%s: cannot open (error %lu)", argv[i], (unsigned long)GetLastError());
            bad++;
            continue;
        }
        if (StartServiceA(svc, 0, NULL)) {
            SERVICE_STATUS st;
            int k;

            /* Started is not the same as running: a driver can fail in DriverEntry after the
             * SCM has accepted the request. */
            for (k = 0; k < 50; k++) {
                if (!QueryServiceStatus(svc, &st))
                    break;
                if (st.dwCurrentState != SERVICE_START_PENDING)
                    break;
                Sleep(100);
            }
            say("%s: start requested -> %s", argv[i], service_state(argv[i]));
        } else if (GetLastError() == ERROR_SERVICE_ALREADY_RUNNING) {
            /* Not a failure, and saying "FAILED" here is actively wrong: a re-run of the pass,
             * or a service another component already brought up, lands here every time. The
             * caller's count of failures decides whether the run-list task reports an error, so
             * counting this would fail a task that had done exactly what was asked. */
            say("%s: already running", argv[i]);
        } else {
            DWORD e = GetLastError();
            const char *why =
                e == ERROR_SERVICE_DISABLED        ? "disabled (Start=4)" :
                e == ERROR_FILE_NOT_FOUND          ? "the driver file is not there" :
                e == ERROR_SERVICE_DEPENDENCY_FAIL ? "a dependency failed" :
                e == ERROR_INVALID_IMAGE_HASH      ? "the image failed validation" :
                e == 1058                          ? "disabled, or no enabled device uses it" :
                e == 1275                          ? "blocked driver" :
                "";

            say("%s: start FAILED (error %lu %s)", argv[i], (unsigned long)e, why);
            bad++;
        }
        CloseServiceHandle(svc);
    }
    CloseServiceHandle(scm);
    return bad ? 1 : 0;
}

/* The HRESULTs this sequence actually returns. Every one is printed raw as well — a name alone
 * is a guess about what a code means, and the codes here are the whole diagnostic.
 *
 * Note the first two: NETCFG_S_REBOOT and NETCFG_E_ALREADY_INITIALIZED differ only in the
 * severity bit (0x0004A020 against 0x8004A020) and mean opposite things. Reading one for the
 * other turns a success into a puzzle. */
static const char *hresult_name(HRESULT hr)
{
    switch ((unsigned long)hr) {
    case 0x00000000UL: return "S_OK";
    case 0x00000001UL: return "S_FALSE";
    case 0x0004A020UL: return "NETCFG_S_REBOOT (success - a PE never reboots, so nothing to do)";
    case 0x0004A024UL: return "NETCFG_S_CAUSED_SETUP_CHANGE";
    case 0x0004A025UL: return "NETCFG_S_COMMIT_NOW";
    case 0x8004A020UL: return "NETCFG_E_ALREADY_INITIALIZED";
    case 0x8004A021UL: return "NETCFG_E_NOT_INITIALIZED";
    case 0x8004A022UL: return "NETCFG_E_IN_USE";
    case 0x8004A024UL: return "NETCFG_E_NO_WRITE_LOCK";
    case 0x8004A025UL: return "NETCFG_E_NEED_REBOOT";
    case 0x8004A027UL: return "NETCFG_E_ADAPTER_NOT_FOUND (no NIC - did `pehw devices` run?)";
    case 0x80040154UL: return "REGDB_E_CLASSNOTREG (netcfgx.dll is not registered)";
    /* The three out-of-process activation failures, which read alike and mean different things:
     * the launcher gave up waiting for the server to register its class object; the server could
     * not be reached at all; the server went away mid-call. The first is named without being
     * interpreted — "the service never served" is the common reading, but running before the
     * service stack is up produces the same code, and this file does not put a guess in front of
     * a raw value. */
    case 0x80080005UL: return "CO_E_SERVER_EXEC_FAILURE (launcher timed out waiting for the "
                              "server's class object)";
    case 0x800706BAUL: return "RPC_S_SERVER_UNAVAILABLE (the server could not be reached)";
    case 0x80010108UL: return "RPC_E_DISCONNECTED (the server died during the call)";
    case 0x80004001UL: return "E_NOTIMPL";
    case 0x80004002UL: return "E_NOINTERFACE";
    case 0x80004005UL: return "E_FAIL";
    case 0x80070005UL: return "E_ACCESSDENIED";
    case 0x8007000EUL: return "E_OUTOFMEMORY";
    case 0x80070002UL: return "ERROR_FILE_NOT_FOUND (the component INF is not on the search path)";
    /* setupapi errors reach COM as HRESULTs in facility 15: 0x800F0nnn is setupapi's 0xE0000nnn.
     * The two that matter here are one apart and mean very different things. */
    case 0x800F0201UL: return "ERROR_CLASS_MISMATCH - asked the wrong network class to install it";
    case 0x800F0202UL: return "ERROR_DUPLICATE_FOUND - already installed?";
    case 0x800F0203UL: return "ERROR_NO_DRIVER_SELECTED";
    case 0x800F020AUL: return "ERROR_NO_INF - the component's INF is not on the search path";
    case 0x800F0217UL: return "no associated service - an INF problem";
    case 0xE0000217UL: return "no associated service - an INF problem";
    default:           return "";
    }
}

/* netcfgx.dll is a self-registering COM server, and nothing in a PE ever registers it. XP setup
 * does it from syssetup.inf's [EarlyOleControlDlls] (`11,,netcfgx.dll,1`), one of the setup
 * passes a /minint boot never runs — so CLSID_CNetCfg is absent from every hive we build, from
 * the source's own hivecls.inf/hivesft.inf, and from Microsoft's WinPE SOFTWARE hive too. Left
 * unregistered, CoCreateInstance returns REGDB_E_CLASSNOTREG and every later step is noise.
 *
 * Registered here rather than through the shell's COM registration contract, so that a console
 * build gets networking without pulling in a desktop. See README.md § The network stack. */
static int register_netcfgx(void)
{
    typedef HRESULT (WINAPI *DLLREGISTER)(void);
    HMODULE m;
    DLLREGISTER reg;
    HRESULT hr;

    WHERE("loading netcfgx.dll");
    m = LoadLibraryA("netcfgx.dll");
    if (!m) {
        say("netcfgx.dll did not load (error %lu) - is it staged?",
            (unsigned long)GetLastError());
        return 0;
    }
    reg = (DLLREGISTER)(void(*)(void))GetProcAddress(m, "DllRegisterServer");
    if (!reg) {
        say("netcfgx.dll exports no DllRegisterServer");
        /* cppcheck-suppress resourceLeak ; held for the process lifetime, see below */
        return 0;
    }

    WHERE("registering netcfgx.dll");
    hr = reg();
    say("netcfgx.dll DllRegisterServer -> 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
    /* Never freed, deliberately: this module is the in-process server for the class just
     * registered, and CoCreateInstance is about to load it again anyway. */
    /* cppcheck-suppress resourceLeak ; see above */
    return SUCCEEDED(hr);
}

/* Which network class a component belongs to.
 *
 * There are four, and `INetCfgClassSetup` is obtained per class: asking the wrong one to install
 * a component fails with `ERROR_CLASS_MISMATCH` (0x800F0201) and nothing else — no hint that the
 * class is the problem. `ms_tcpip` is **NetTrans**, not Net: `Net` is the adapters, and a
 * protocol only looks like it belongs there. `nettcpip.inf` says so itself
 * (`Class = NetTrans`, `{4d36e975-…}`) against `netrtsnt.inf`'s `Class = Net` `{4d36e972-…}`.
 *
 * The ids below are `netcfgx.h`'s own `NETCFG_*_CID_MS_*` constants, whose names carry the class
 * — so this table is transcribed rather than guessed. A component not listed here can still be
 * installed by naming its class: `trans:ms_foo`, `client:`, `service:`, `adapter:`. */
static const struct { const char *id; const GUID *cls; } net_classes[] = {
    { "ms_tcpip",     &GUID_DEVCLASS_NETTRANS },
    { "ms_netbeui",   &GUID_DEVCLASS_NETTRANS },
    { "ms_nwipx",     &GUID_DEVCLASS_NETTRANS },
    { "ms_nwspx",     &GUID_DEVCLASS_NETTRANS },
    { "ms_appletalk", &GUID_DEVCLASS_NETTRANS },
    { "ms_netmon",    &GUID_DEVCLASS_NETTRANS },
    { "ms_msclient",  &GUID_DEVCLASS_NETCLIENT },
    { "ms_server",    &GUID_DEVCLASS_NETSERVICE },
    { "ms_netbios",   &GUID_DEVCLASS_NETSERVICE },
    { "ms_pschedpc",  &GUID_DEVCLASS_NETSERVICE },
    { "ms_wlbs",      &GUID_DEVCLASS_NETSERVICE },
};

/* Resolve a component argument to its class and its bare id. An explicit `<class>:<id>` prefix
 * wins; otherwise the table above decides. Returns NULL for an unknown id, which is reported
 * rather than guessed at — installing against the wrong class is the failure this exists to
 * prevent, so defaulting to one would just reintroduce it. */
static const GUID *net_class_of(const char *arg, const char **id)
{
    static const struct { const char *pfx; const GUID *cls; } prefixes[] = {
        { "trans:",   &GUID_DEVCLASS_NETTRANS },
        { "client:",  &GUID_DEVCLASS_NETCLIENT },
        { "service:", &GUID_DEVCLASS_NETSERVICE },
        { "adapter:", &GUID_DEVCLASS_NET },
    };
    size_t i;

    for (i = 0; i < sizeof prefixes / sizeof prefixes[0]; i++) {
        int n = lstrlenA(prefixes[i].pfx);

        if (CompareStringA(LOCALE_INVARIANT, NORM_IGNORECASE, arg, n,
                           prefixes[i].pfx, n) == CSTR_EQUAL) {
            *id = arg + n;
            return prefixes[i].cls;
        }
    }
    for (i = 0; i < sizeof net_classes / sizeof net_classes[0]; i++)
        if (lstrcmpiA(arg, net_classes[i].id) == 0) {
            *id = arg;
            return net_classes[i].cls;
        }
    *id = arg;
    return NULL;
}

/* Wait for a service to reach RUNNING, bounded. Returns 1 if it got there.
 *
 * The network task runs at pre-shell, early enough that the machine is still assembling itself.
 * `pehw devices` already guards against this with CMP_WaitServicesAvailable; the component
 * install has its own prerequisite, because INetCfg is COM and COM needs the RPC service
 * subsystem. Started too early it fails somewhere inside Initialize, and — before the summary
 * was fixed — reported that as a task that had done nothing wrong.
 *
 * A wait rather than a later Order: moving the task later would put it after the device pass,
 * which is precisely the ordering that does not bind. */
static int wait_service_running(const char *name, DWORD timeout_ms)
{
    SC_HANDLE scm, svc;
    DWORD waited = 0;
    int running = 0;

    scm = OpenSCManagerA(NULL, NULL, SC_MANAGER_CONNECT);
    if (!scm)
        return 0;
    for (;;) {
        SERVICE_STATUS st;

        svc = OpenServiceA(scm, name, SERVICE_QUERY_STATUS);
        if (svc) {
            if (QueryServiceStatus(svc, &st) && st.dwCurrentState == SERVICE_RUNNING)
                running = 1;
            CloseServiceHandle(svc);
        }
        if (running || waited >= timeout_ms)
            break;
        Sleep(250);
        waited += 250;
    }
    CloseServiceHandle(scm);
    if (waited)
        say("waited %lu ms for %s: %s", (unsigned long)waited, name,
            running ? "RUNNING" : "NOT running - continuing anyway");
    return running;
}

/* Whether the SCM knows this service at all. A co-service list can name one this image never
 * defines — 3790's names Smb, which belongs to a component we do not install — and reporting
 * that as a failed start describes the image as broken when it is merely smaller. */
static int service_exists(const char *name)
{
    SC_HANDLE scm, svc;
    int ok;

    scm = OpenSCManagerA(NULL, NULL, SC_MANAGER_CONNECT);
    if (!scm)
        return 1;   /* cannot tell from here; let the start attempt report whatever it finds */
    svc = OpenServiceA(scm, name, SERVICE_QUERY_CONFIG);
    if (svc) {
        ok = 1;
        CloseServiceHandle(svc);
    } else {
        ok = GetLastError() != ERROR_SERVICE_DOES_NOT_EXIST;
    }
    CloseServiceHandle(scm);
    return ok;
}

/* One co-service: skip what the image does not define, then start it. */
static void start_one(const char *name)
{
    char *argv[3];

    if (!service_exists(name)) {
        say("  %s: not defined in this image - skipped", name);
        return;
    }
    argv[2] = (char *)name;
    cmd_start(3, argv);
}

/* Start the services an installed component declares, because in a PE nothing else will.
 *
 * The adapted INF excludes every co-service from setup-time starting, and the protocol is
 * Start=1 (system) — loaded by the I/O manager at boot, which for a PE never comes round again.
 * More than convenience: the adapter only binds if the protocol is already **running** when the
 * NIC registers with NDIS, so this is a required step of the install, not a tidy-up after it.
 *
 * The list is the component's own `Ndi\Service` and `Ndi\CoServices`, read from the key INetCfg
 * just wrote — declared by the INF rather than hardcoded here, so a component other than
 * ms_tcpip brings its own answer. Dependencies are left to the SCM: starting Tcpip pulls IPSec
 * in by itself, which is why no order is imposed.
 *
 * Every result is reported and none is fatal. Several co-services are svchost/lsass win32
 * services a minimal PE may legitimately not run, and a network stack that works without them
 * should not be reported as a failed install. */
static void start_component_services(INetCfgComponent *comp, const GUID *cls)
{
    GUID inst;
    wchar_t key[512], cls_s[64], inst_s[64];
    HKEY k;
    char svc[256];
    BYTE multi[4096];
    DWORD type = 0, cb;

    if (FAILED(INetCfgComponent_GetInstanceGuid(comp, &inst)))
        return;
    StringFromGUID2(cls, cls_s, sizeof cls_s / sizeof cls_s[0]);
    StringFromGUID2(&inst, inst_s, sizeof inst_s / sizeof inst_s[0]);
    wsprintfW(key, L"SYSTEM\\CurrentControlSet\\Control\\Network\\%s\\%s\\Ndi", cls_s, inst_s);

    if (RegOpenKeyExW(HKEY_LOCAL_MACHINE, key, 0, KEY_READ, &k) != ERROR_SUCCESS) {
        say("  (no Ndi key for the component - nothing started)");
        return;
    }

    svc[0] = 0;
    cb = sizeof svc;
    if (RegQueryValueExA(k, "Service", NULL, &type, (BYTE *)svc, &cb) == ERROR_SUCCESS && svc[0]) {
        say("  starting %s (the component's own service)", svc);
        start_one(svc);
    }

    cb = sizeof multi;
    if (RegQueryValueExA(k, "CoServices", NULL, &type, multi, &cb) == ERROR_SUCCESS
        && type == REG_MULTI_SZ) {
        char *p = (char *)multi;
        DWORD used = 0;

        while (used < cb && *p) {
            int n = lstrlenA(p);

            if (lstrcmpiA(p, svc) != 0)
                start_one(p);
            p += n + 1;
            used += n + 1;
        }
    }
    RegCloseKey(k);
}

/* Install network components — the half of a NIC's setup that the device pass does not do.
 *
 * `pehw devices` binds a driver to the adapter and stops there: no NetCfgInstanceId, no Linkage,
 * no protocol above it and nothing bound to it, so the devnode never starts (PnP problem 31).
 * That configuration is INetCfg's job — the interface `netcfg -winpe` drives — and it writes the
 * protocol service definitions itself as part of installing a component. Which is precisely why
 * this plugin does not bake them: registry state is the *output* of an install, and a
 * hand-written approximation of it is what made the miniport load and unload again.
 *
 * Components are named by their INF id (`ms_tcpip`), not by service or file name. */
static int cmd_net(int argc, char **argv)
{
    INetCfg *cfg = NULL;
    INetCfgLock *lock = NULL;
    INetCfgClassSetup *setup = NULL;
    LPWSTR held_by = NULL;
    HRESULT hr;
    const char *names[16];
    int count = 0, i, ok = 0, bad = 0;
    int co_done = 0, locked = 0, inited = 0;
    /* Whether the install loop was ever reached. Without this an early exit — a held write lock,
     * an Initialize that refused — leaves both counters at zero and the summary reads
     * "0 of 1 installed, 0 failed", which looks like nothing went wrong. Worse, the exit code
     * came from `bad` alone, so the run list recorded an aborted task as ok, and the next task
     * ran against a machine that had no protocol installed. A task that aborted must say so. */
    int reached = 0;

    SetUnhandledExceptionFilter(crash_filter);

    /* Everything after the subcommand that is not a flag is a component id. TCP/IP alone by
     * default: the OPK manual names TCP/IP and NetBIOS-over-TCP/IP as the only connectivity
     * methods tested for a PE, and TCP/IP is the one the adapter has to have. */
    for (i = 2; i < argc && count < (int)(sizeof names / sizeof names[0]); i++)
        if (argv[i][0] != '-')
            names[count++] = argv[i];
    if (!count)
        names[count++] = "ms_tcpip";

    WHERE("enabling install privileges");
    enable_install_privs();

    /* Everything below is COM, and at pre-shell the machine may not have got there yet. Both
     * waits are no-ops once the system is up, so running this by hand costs nothing. */
    WHERE("waiting for the service stack");
    wait_pnp();
    wait_service_running("RpcSs", 60000);

    /* Reported, not fatal. The class may already be registered by another route (the shell
     * registers COM servers on its own contract), in which case this failing changes nothing —
     * and if it is the real problem, CoCreateInstance says so in the very next line. */
    register_netcfgx();

    WHERE("CoInitializeEx");
    hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(hr)) {
        say("net: CoInitializeEx failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        return 1;
    }
    co_done = 1;

    WHERE("creating the INetCfg instance");
    hr = CoCreateInstance(&CLSID_CNetCfg, NULL, CLSCTX_INPROC_SERVER, &IID_INetCfg, (void **)&cfg);
    if (FAILED(hr)) {
        say("net: CoCreateInstance(CNetCfg) failed 0x%08lX %s", (unsigned long)hr,
            hresult_name(hr));
        goto out;
    }

    /* The write lock is a real cross-process lock, not a formality: INetCfg refuses to
     * Initialize for writing without it. S_FALSE means somebody else holds it and names them,
     * which is a different problem from any failure code and has to read differently. */
    WHERE("acquiring the INetCfg write lock");
    hr = INetCfg_QueryInterface(cfg, &IID_INetCfgLock, (void **)&lock);
    if (FAILED(hr)) {
        say("net: no INetCfgLock 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        goto out;
    }
    hr = INetCfgLock_AcquireWriteLock(lock, 15000, L"pehw", &held_by);
    if (hr == S_FALSE) {
        say("net: the write lock is held by %ls", held_by ? held_by : L"(unnamed)");
        CoTaskMemFree(held_by);
        goto out;
    }
    if (FAILED(hr)) {
        say("net: AcquireWriteLock failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        goto out;
    }
    locked = 1;

    WHERE("INetCfg::Initialize");
    hr = INetCfg_Initialize(cfg, NULL);
    if (FAILED(hr)) {
        say("net: Initialize failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        goto out;
    }
    inited = 1;

    reached = 1;
    for (i = 0; i < count; i++) {
        INetCfgComponent *comp = NULL;
        const GUID *cls;
        const char *id8;
        OBO_TOKEN obo;
        wchar_t id[128];

        /* Per component, not once for the run: the class is a property of the component, and two
         * components in one invocation need not share it. */
        cls = net_class_of(names[i], &id8);
        if (!cls) {
            say("net: %s - unknown component; name its class as trans:%s (or client:/service:/"
                "adapter:)", names[i], names[i]);
            bad++;
            continue;
        }

        WHERE("querying the class setup interface for %s", id8);
        hr = INetCfg_QueryNetCfgClass(cfg, cls, &IID_INetCfgClassSetup, (void **)&setup);
        if (FAILED(hr)) {
            say("net: QueryNetCfgClass for %s failed 0x%08lX %s", id8, (unsigned long)hr,
                hresult_name(hr));
            bad++;
            continue;
        }

        /* A component id longer than the buffer makes this fail and write nothing, leaving
         * `id` as whatever was on the stack — which would then be handed to Install as the
         * thing to install. An over-long id is a bad argument; say so and move on. */
        if (!MultiByteToWideChar(CP_ACP, 0, id8, -1, id, sizeof id / sizeof id[0])) {
            say("net: %s - component id is not usable (error %lu)", id8,
                (unsigned long)GetLastError());
            INetCfgClassSetup_Release(setup);
            setup = NULL;
            bad++;
            continue;
        }

        /* On behalf of the user: the install is being asked for directly, not pulled in as
         * another component's dependency, which is what OBO_COMPONENT would mean. */
        ZeroMemory(&obo, sizeof obo);
        obo.Type = OBO_USER;

        /* Printed before the call, because this is the one that can hang or fault — if it does,
         * this line is the last thing on screen and the whole diagnostic. */
        say("installing %s", names[i]);
        WHERE("installing the %s component", names[i]);

        /* The four middle arguments are an interactive-equivalent install with no answer file.
         * They are the least-evidenced part of this sequence: the interface shapes come from the
         * headers, but nothing traced says what a PE install passes here. */
        hr = INetCfgClassSetup_Install(setup, id, &obo, 0, 0, NULL, NULL, &comp);
        say("  %s -> 0x%08lX %s", names[i], (unsigned long)hr, hresult_name(hr));
        if (SUCCEEDED(hr))
            ok++;
        else
            bad++;
        if (comp)
            INetCfgComponent_Release(comp);
        INetCfgClassSetup_Release(setup);
        setup = NULL;      /* so the cleanup below does not release it a second time */
    }

    /* Nothing above is written until Apply. An install that reports success and is then not
     * applied leaves the registry exactly as it was, which reads as the install having silently
     * done nothing. */
    WHERE("INetCfg::Apply");
    hr = INetCfg_Apply(cfg);
    say("apply -> 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
    if (FAILED(hr))
        bad++;

    /* Start what was just installed, before any adapter is introduced. The order matters and is
     * the whole reason this runs here: NDIS binds a protocol to a miniport when one of them
     * registers, and only the adapter-registration path works in a PE — so the protocol has to
     * be running before the NIC appears. See README.md § The order of the two passes. */
    if (SUCCEEDED(hr)) {
        for (i = 0; i < count; i++) {
            const GUID *cls;
            const char *id8;
            INetCfgComponent *comp = NULL;
            wchar_t id[128];

            cls = net_class_of(names[i], &id8);
            if (!cls)
                continue;
            if (!MultiByteToWideChar(CP_ACP, 0, id8, -1, id, sizeof id / sizeof id[0]))
                continue;   /* already reported when the install was attempted */
            if (SUCCEEDED(INetCfg_FindComponent(cfg, id, &comp)) && comp) {
                say("starting the services %s declares", id8);
                start_component_services(comp, cls);
                INetCfgComponent_Release(comp);
            }
        }
    }

out:
    if (setup)
        INetCfgClassSetup_Release(setup);
    if (inited)
        INetCfg_Uninitialize(cfg);
    if (locked)
        INetCfgLock_ReleaseWriteLock(lock);
    if (lock)
        INetCfgLock_Release(lock);
    if (cfg)
        INetCfg_Release(cfg);
    if (co_done)
        CoUninitialize();

    /* One self-contained line: at boot the runner keeps only the last line of output as the
     * task's Message, so anything that has to reach a human belongs here — including, above all,
     * the fact that the run never got as far as installing anything. */
    if (!reached)
        say("net: ABORTED before installing anything - see the step above; nothing is configured");
    else
        say("net: %d of %d component(s) installed, %d failed", ok, count, bad);
    return (bad || !reached) ? 1 : 0;
}

/* What the IP layer itself thinks it has.
 *
 * Registry state says the adapter is bound: Tcpip's Linkage names its device. That is the
 * *configuration*, written by the install. Whether NDIS actually bound the protocol to the
 * miniport at run time is a different fact, and nothing in the registry records it. Asking IP
 * Helper for the adapter list is asking the stack directly.
 *
 * Resolved at run time rather than linked, like every other optional entry point here, so pehw
 * still loads in a build that stages no networking at all.
 *
 *     DWORD GetAdaptersInfo(IP_ADAPTER_INFO *, ULONG *);
 *     ERROR_NO_DATA / ERROR_BUFFER_OVERFLOW are both normal answers and mean opposite things.
 */
/* Held for the process lifetime rather than freed on each exit path: it is loaded once, the
 * process is short-lived, and unloading IP Helper mid-report buys nothing. */
static HMODULE g_iphlpapi;

typedef DWORD (WINAPI *GETADAPTERSINFO)(IP_ADAPTER_INFO *, ULONG *);
typedef DWORD (WINAPI *GETINTERFACEINFO)(IP_INTERFACE_INFO *, ULONG *);
typedef DWORD (WINAPI *GETIPADDRTABLE)(MIB_IPADDRTABLE *, ULONG *, WINBOOL);

/* Ask three different questions, because one API's silence is not an answer.
 *
 * GetAdaptersInfo reports ERROR_NO_DATA for "no adapter information", which is not the same
 * claim as "nothing is bound" — reading it that way would be a diagnostic encoding a guess.
 * GetInterfaceInfo lists the interfaces TCP/IP has bound, address or no address, which is the
 * question actually being asked. GetIpAddrTable is the control: a working stack always has the
 * loopback entry, so an empty table means TCP/IP has no interfaces at all — the protocol never
 * initialised — while a table with only loopback means it initialised and did not get the NIC.
 * Those are different faults and they look identical through GetAdaptersInfo alone. */
static void ip_interfaces(void)
{
    GETINTERFACEINFO gii = (GETINTERFACEINFO)(void(*)(void))
        GetProcAddress(g_iphlpapi, "GetInterfaceInfo");
    GETIPADDRTABLE git = (GETIPADDRTABLE)(void(*)(void))
        GetProcAddress(g_iphlpapi, "GetIpAddrTable");
    ULONG cb = 0;
    DWORD e;

    if (gii) {
        IP_INTERFACE_INFO *ii;

        e = gii(NULL, &cb);
        if (e != ERROR_INSUFFICIENT_BUFFER && e != ERROR_SUCCESS) {
            say("    GetInterfaceInfo: error %lu", (unsigned long)e);
        } else if ((ii = (IP_INTERFACE_INFO *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY,
                                                        cb ? cb : sizeof *ii)) != NULL) {
            if (gii(ii, &cb) == ERROR_SUCCESS) {
                int j;

                say("    bound interfaces: %ld", (long)ii->NumAdapters);
                for (j = 0; j < ii->NumAdapters; j++)
                    say("      index %lu  %ls", (unsigned long)ii->Adapter[j].Index,
                        ii->Adapter[j].Name);
            } else {
                say("    GetInterfaceInfo: error %lu", (unsigned long)GetLastError());
            }
            HeapFree(GetProcessHeap(), 0, ii);
        }
    }

    if (git) {
        MIB_IPADDRTABLE *t;

        cb = 0;
        e = git(NULL, &cb, FALSE);
        if (e != ERROR_INSUFFICIENT_BUFFER && e != ERROR_SUCCESS) {
            say("    GetIpAddrTable: error %lu", (unsigned long)e);
        } else if ((t = (MIB_IPADDRTABLE *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY,
                                                     cb ? cb : sizeof *t)) != NULL) {
            if (git(t, &cb, FALSE) == ERROR_SUCCESS) {
                DWORD j;

                say("    ip address table: %lu entr%s", (unsigned long)t->dwNumEntries,
                    t->dwNumEntries == 1 ? "y" : "ies");
                for (j = 0; j < t->dwNumEntries; j++) {
                    DWORD a = t->table[j].dwAddr;

                    say("      %lu.%lu.%lu.%lu  if index %lu",
                        (unsigned long)(a & 0xFF), (unsigned long)((a >> 8) & 0xFF),
                        (unsigned long)((a >> 16) & 0xFF), (unsigned long)((a >> 24) & 0xFF),
                        (unsigned long)t->table[j].dwIndex);
                }
                if (!t->dwNumEntries)
                    say("      (not even loopback - TCP/IP has no interfaces at all)");
            } else {
                say("    GetIpAddrTable: error %lu", (unsigned long)GetLastError());
            }
            HeapFree(GetProcessHeap(), 0, t);
        }
    }
}

/* --- `pehw netconn`: the connection, from both ends -----------------------------------------
 * Read-only. Why both halves are reported rather than one: README.md § Reading the connection
 * state. */

static const char *netcon_status_name(NETCON_STATUS s)
{
    switch (s) {
    case NCS_DISCONNECTED:             return "disconnected";
    case NCS_CONNECTING:               return "connecting";
    case NCS_CONNECTED:                return "CONNECTED";
    case NCS_DISCONNECTING:            return "disconnecting";
    case NCS_HARDWARE_NOT_PRESENT:     return "hardware not present";
    case NCS_HARDWARE_DISABLED:        return "hardware disabled";
    case NCS_HARDWARE_MALFUNCTION:     return "hardware malfunction";
    case NCS_MEDIA_DISCONNECTED:       return "media disconnected (cable?)";
    case NCS_AUTHENTICATING:           return "authenticating";
    case NCS_AUTHENTICATION_SUCCEEDED: return "authenticated";
    case NCS_AUTHENTICATION_FAILED:    return "authentication FAILED";
    case NCS_INVALID_ADDRESS:          return "invalid address";
    case NCS_CREDENTIALS_REQUIRED:     return "credentials required";
    default:                           return "?";
    }
}

static const char *netcon_media_name(NETCON_MEDIATYPE m)
{
    switch (m) {
    case NCM_NONE:   return "none";
    case NCM_DIRECT: return "direct";
    case NCM_ISDN:   return "isdn";
    case NCM_LAN:    return "LAN";
    case NCM_PHONE:  return "phone";
    case NCM_TUNNEL: return "tunnel";
    case NCM_PPPOE:  return "pppoe";
    case NCM_BRIDGE: return "bridge";
    default:         return "?";
    }
}

/* Print a wide string through say(), which is printf-family.
 *
 * %ls is not usable for this: msvcrt converts it in the C locale, which nothing sets, so the first
 * code point above 0xFF fails to convert and printf stops emitting there. A localized source names
 * its connection in the local script, so the name — the field this subcommand exists to show —
 * would come out empty. Converting to the console's own code page avoids that, and also avoids the
 * mojibake a setlocale() would give instead, since the ANSI and console code pages differ on those
 * systems. */
static const char *wide_str(const WCHAR *w, char *buf, int cap)
{
    if (cap <= 0)
        return "(unprintable)";
    if (!w)
        return "(none)";
    if (!WideCharToMultiByte(GetConsoleOutputCP(), 0, w, -1, buf, cap, NULL, NULL)) {
        /* Too long and unconvertible are different findings, and folding them together would
         * point the reader at a rendering fault when the value is simply longer than the buffer. */
        return GetLastError() == ERROR_INSUFFICIENT_BUFFER ? "(too long)" : "(unprintable)";
    }
    buf[cap - 1] = 0;
    return buf;
}

/* Read a REG_SZ and render it for the console, always NUL-terminated.
 *
 * Read through the *wide* API deliberately. RegQueryValueExA would return the value converted to
 * the ANSI code page, which is not the console's on a localized system — printing it gives
 * mojibake, in the same way %ls gives nothing. Taking the wide value and converting once, to the
 * console code page, is the only path that is right on both.
 *
 * RegQueryValueEx also does not promise termination, and will return a non-string type into the
 * same bytes; neither is trusted here. */
static const char *reg_string(HKEY k, const WCHAR *value, char *buf, DWORD cap)
{
    WCHAR w[512];
    DWORD sz = sizeof w, type = 0;
    LONG r = RegQueryValueExW(k, value, NULL, &type, (BYTE *)w, &sz);

    /* "absent" is itself the finding here — it is how a Connection entry the class installer never
     * finished writing shows up — so only a genuinely missing value may report it. One that is
     * present but unreadable (longer than the buffer, wrong type, access denied) says that
     * instead, rather than being folded into the conclusion it would falsely support. */
    if (r == ERROR_FILE_NOT_FOUND)
        return "(absent)";
    if (r != ERROR_SUCCESS)
        return "(unreadable)";
    if (type != REG_SZ && type != REG_EXPAND_SZ)
        return "(not a string)";
    /* Present but empty is its own answer: without this it prints as a blank field, which reads
     * like pehw failed to render it rather than like the installer wrote nothing. */
    if (sz < sizeof(WCHAR))
        return "(empty)";
    /* RegQueryValueEx does not promise termination: a value written without its NUL comes back
     * exactly filling the buffer. sz is in bytes. */
    w[sz / sizeof(WCHAR) < (sizeof w / sizeof(WCHAR))
        ? sz / sizeof(WCHAR) : (sizeof w / sizeof(WCHAR)) - 1] = 0;
    return wide_str(w, buf, (int)cap);
}


/* The per-connection key the Net class installer writes, and that netman reads its settings back
 * from. The shape is netman's, not ours: {4D36E972-...} is the Net device class and the leaf name
 * is fixed. Shared by the report and by the tray-icon pass so the two cannot drift apart. */
static void connection_key_path(const char *guid, char *out, DWORD cap)
{
    _snprintf(out, cap - 1,
              "SYSTEM\\CurrentControlSet\\Control\\Network"
              "\\{4D36E972-E325-11CE-BFC1-08002BE10318}\\%s\\Connection", guid);
    out[cap - 1] = 0;
}

/* One connection's persisted registry entry. A missing entry is said out loud: it is a finding,
 * not an absence of output. */
static void report_connection_key(const char *guid)
{
    char sub[256];
    HKEY k;
    char buf[256];
    DWORD sz, type, d;

    connection_key_path(guid, sub, sizeof sub);
    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, sub, 0, KEY_READ, &k) != ERROR_SUCCESS) {
        say("  %s", guid);
        say("    NO Connection subkey - the Net class installer did not write one");
        return;
    }
    say("  %s", guid);
    say("    Name          = %s", reg_string(k, L"Name", buf, sizeof buf));
    say("    PnpInstanceID = %s", reg_string(k, L"PnpInstanceID", buf, sizeof buf));
    sz = sizeof d;
    /* Absent is the interesting case: netman reads an absent value as "hidden" rather than
     * defaulting it, so this separates "never set" from "set and ignored". */
    if (RegQueryValueExA(k, "ShowIcon", NULL, &type, (BYTE *)&d, &sz) == ERROR_SUCCESS
        && type == REG_DWORD)
        say("    ShowIcon      = %lu%s", (unsigned long)d, d ? "" : "  (icon hidden)");
    else
        say("    ShowIcon      = (absent)  - no tray icon for this connection");
    RegCloseKey(k);
}

/* What netman enumerates — a live COM call into the service, not a registry read, so a failure
 * here is the one the folder, the tray icon and the Status dialog each hit in their own way. */
static void report_netman(void)
{
    INetConnectionManager *mgr = NULL;
    IEnumNetConnection *e = NULL;
    INetConnection *c = NULL;
    HRESULT hr;
    ULONG got = 0;
    int n = 0;
    char nbuf[256], dbuf[256];

    hr = CoCreateInstance(&pehw_CLSID_ConnectionManager, NULL, CLSCTX_ALL,
                          &pehw_IID_INetConnectionManager, (void **)&mgr);
    if (FAILED(hr)) {
        say("netman: CoCreateInstance(ConnectionManager) failed 0x%08lX %s",
            (unsigned long)hr, hresult_name(hr));
        say("  the shell's network UI talks to this object - with it unreachable, the folder is"
            " empty and no dialog opens");
        return;
    }
    hr = INetConnectionManager_EnumConnections(mgr, NCME_DEFAULT, &e);
    if (FAILED(hr)) {
        say("netman: EnumConnections failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        INetConnectionManager_Release(mgr);
        return;
    }
    /* Test the pointer the call actually fills, not pceltFetched: an enumerator asked for exactly
     * one item may leave that count untouched (the IEnumXxx contract even allows a NULL pointer
     * for it there). Testing it would then end the walk before the first item, reporting no
     * connections while netman was enumerating them fine — and leaking the one already handed
     * back. `got` is kept only because the call needs somewhere to write. */
    for (;;) {
        NETCON_PROPERTIES *p = NULL;

        c = NULL;
        if (IEnumNetConnection_Next(e, 1, &c, &got) != S_OK || !c)
            break;
        n++;
        hr = INetConnection_GetProperties(c, &p);
        if (SUCCEEDED(hr) && p) {
            say("  \"%s\"", wide_str(p->pszwName, nbuf, sizeof nbuf));
            say("    device      = %s", wide_str(p->pszwDeviceName, dbuf, sizeof dbuf));
            say("    status      = %s", netcon_status_name(p->Status));
            say("    media       = %s", netcon_media_name(p->MediaType));
            /* NCCF_SHOW_ICON is derived from the registry ShowIcon above, so decoding it here
             * lets the two be compared: disagreement localises the fault to netman. */
            say("    character   = 0x%08lX%s", (unsigned long)p->dwCharacter,
                (p->dwCharacter & NCCF_SHOW_ICON) ? "  (NCCF_SHOW_ICON set)"
                                                  : "  (NCCF_SHOW_ICON CLEAR - no tray icon)");
            /* The class the shell instantiates to raise this connection's Status and Properties
             * dialogs — in-process, so a failure mode of its own. Exercised here because the
             * shell reports nothing when it cannot raise a dialog: double-clicking simply does
             * nothing. Creating the same class by hand turns that into an HRESULT.
             *
             * NOTE this is the one step of `netconn` that is not purely a read: it loads a
             * third-party in-process server and runs its DllMain and constructor, on an STA with
             * no message pump. Harmless against netshell, but it is why this subcommand must stay
             * a by-hand tool and must not be added to the startup run list. */
            {
                IUnknown *ui = NULL;
                HRESULT u;
                wchar_t g[64];
                char gbuf[64];

                if (StringFromGUID2(&p->clsidUiObject, g, 64))
                    say("    UI class    = %s", wide_str(g, gbuf, sizeof gbuf));
                /* A connection with no UI class of its own is a fact about the connection, not a
                 * registration fault — creating CLSID_NULL would report REGDB_E_CLASSNOTREG and
                 * send the reader after a problem that does not exist. */
                if (IsEqualGUID(&p->clsidUiObject, &pehw_GUID_NULL)) {
                    say("    UI object   = (this connection declares no UI class)");
                } else {
                    u = CoCreateInstance(&p->clsidUiObject, NULL, CLSCTX_INPROC_SERVER,
                                         &pehw_IID_IUnknown, (void **)&ui);
                    if (SUCCEEDED(u) && ui) {
                        say("    UI object   = created OK");
                        ui->lpVtbl->Release(ui);
                    } else if (SUCCEEDED(u)) {
                        say("    UI object   = created nothing (0x%08lX %s)",
                            (unsigned long)u, hresult_name(u));
                    } else {
                        say("    UI object   = FAILED 0x%08lX %s - this is what the Status and"
                            " Properties dialogs are raised from",
                            (unsigned long)u, hresult_name(u));
                    }
                }
            }
            /* NETCON_PROPERTIES and its two strings are one allocation chain owned by the
             * caller; the strings go first because freeing the struct loses the pointers.
             *
             * netcon.h declares NcFreeNetconProperties for this, which no MinGW-w64 import library
             * exports — using it would mean resolving it by hand at run time. Freeing the members
             * is what the marshalled case requires anyway: MIDL allocated each string separately
             * on this side, and netman is always out-of-process here (its AppID is a LocalService),
             * so the single-block layout that would make this wrong cannot arise. */
            CoTaskMemFree(p->pszwName);
            CoTaskMemFree(p->pszwDeviceName);
            CoTaskMemFree(p);
        } else if (hr == S_OBJECT_NO_LONGER_VALID) {
            /* A *success* code (0x00000002, declared in netcon.h beside NETCON_PROPERTIES), so
             * SUCCEEDED() is true while nothing is returned: the connection went away between the
             * enumeration and this call. Reporting it as a failure would name an HRESULT that
             * hresult_name cannot look up, for something that is not a fault. */
            say("  (a connection that went away during the walk)");
        } else if (SUCCEEDED(hr)) {
            /* Any other success that returned no properties. Calling this "failed 0x00000000
             * S_OK" would be self-contradicting output. */
            say("  (a connection whose GetProperties returned nothing, 0x%08lX %s)",
                (unsigned long)hr, hresult_name(hr));
        } else {
            say("  (a connection whose GetProperties failed 0x%08lX %s)",
                (unsigned long)hr, hresult_name(hr));
        }
        INetConnection_Release(c);
        c = NULL;
    }
    IEnumNetConnection_Release(e);
    INetConnectionManager_Release(mgr);
    if (!n)
        say("  netman enumerates NO connections");
    else
        say("netman enumerates %d connection(s)", n);
}

static int cmd_netconn(void)
{
    static const char *kConnClass =
        "SYSTEM\\CurrentControlSet\\Control\\Network"
        "\\{4D36E972-E325-11CE-BFC1-08002BE10318}";
    HKEY cls;
    HRESULT hr;

    SetUnhandledExceptionFilter(crash_filter);

    say("--- registry: Control\\Network\\{4D36E972-...} ---");
    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, kConnClass, 0, KEY_READ, &cls) != ERROR_SUCCESS) {
        say("  the class key itself is MISSING - no network component has ever been installed");
    } else {
        DWORD i;
        int n = 0;

        for (i = 0;; i++) {
            char guid[64];
            DWORD len = sizeof guid;

            LONG r = RegEnumKeyExA(cls, i, guid, &len, NULL, NULL, NULL, NULL);

            /* A failure leaves `guid` untouched, so nothing below may look at it. The two cases
             * are separated deliberately: ERROR_MORE_DATA is about this one index (a name too
             * long for the buffer) so the walk goes on, while anything else is about the key or
             * the handle and would repeat for every index — continuing on that would spin. */
            if (r == ERROR_MORE_DATA)
                continue;
            if (r != ERROR_SUCCESS)
                break;
            /* Descriptions and similar bookkeeping keys live here too; only a GUID-shaped name
             * is a candidate connection, and report_connection_key says whether it really is. */
            if (guid[0] != '{')
                continue;
            n++;
            report_connection_key(guid);
        }
        RegCloseKey(cls);
        if (!n)
            say("  no adapter subkeys at all - the class installer wrote nothing");
    }

    say("");
    hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(hr)) {
        say("  CoInitializeEx failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        return 1;
    }

    say("--- netman: INetConnectionManager ---");
    report_netman();
    CoUninitialize();
    return 0;
}

/* How long to wait for the Net class installer to write a connection's key, and how often to
 * look. The wait exists because it creates that key well after the install call that produced it
 * has returned — measured at more than ten seconds, which is why the network pass cannot simply
 * do this at its own end. Running post-shell spends far more than that budget before this starts,
 * so the wait is insurance for a slow machine rather than the mechanism, and on a normal boot the
 * key is already there on the first look.
 *
 * Kept short even so: an image with no adapter bound has no key to wait for and would spend all
 * of it, every boot. Fifteen seconds buys the slow case without making the empty case cost real
 * time; losing that bet costs the icon, which the connection's Properties can still turn on by
 * hand. */
#define TRAYICON_WAIT_MS   15000
#define TRAYICON_POLL_MS   1000

/* How long to wait for the notification area's own network object, and how often to look.
 * Short poll: it normally arrives a second or two after the shell paints, and every millisecond
 * of this is spent on the shell's splash. */
#define TRAYWND_WAIT_MS    20000
#define TRAYWND_POLL_MS    250

/* Wait until the component that would draw the icon exists.
 *
 * Telling netman a connection changed only reaches whoever has registered to hear it, and the
 * listener here is netshell's tray object, which the shell brings up a little after its taskbar
 * appears — after the point the run list calls "the desktop is up". Notifying before then is
 * delivered to nobody and reported as success, which is the whole reason this wait exists.
 * Measured on x86: without it the icon came up on two boots in ten, with it on every one.
 *
 * `Connections Tray` is the window class netshell registers for that object, and it creates the
 * window in the same routine, so the window existing is the closest observable stand-in for "the
 * listener is there". Chosen over the notification area's other start-up signals deliberately:
 * `stobject.dll` creates a named event when its hot-plug module starts, which is tempting and
 * wrong, because that module is one bit of a mask a build may legitimately clear, and the network
 * object is created whatever that mask says.
 *
 * Returns non-zero if the window appeared. A miss is not fatal: the notification is best effort. */
static const wchar_t kTrayClass[] = L"Connections Tray";

static BOOL CALLBACK tray_class_cb(HWND h, LPARAM p)
{
    wchar_t cls[32];

    if (GetClassNameW(h, cls, sizeof cls / sizeof cls[0]) && lstrcmpiW(cls, kTrayClass) == 0) {
        *(int *)p = 1;
        return FALSE;
    }
    return TRUE;
}

/* Two places, because a window is only visible to a process on the same desktop, and the run
 * list's tasks are not on the shell's: they keep the desktop they inherit, deliberately — the
 * network pass installs nothing when started on any other, so the runner must not move them. On
 * the boot path it is therefore the by-name enumeration of the shell's desktop that answers; the
 * plain FindWindow answers when this is run by hand from that desktop, and costs nothing to try
 * first.
 *
 * *seen_on names which one answered, and it is printed rather than dropped: which desktop this
 * ran on is a property of the runner, and a boot whose answer changes is telling you the runner
 * moved. */
static int tray_window_present(const char **seen_on)
{
    HDESK d;
    int found = 0;

    if (FindWindowW(kTrayClass, NULL)) {
        *seen_on = "this desktop";
        return 1;
    }
    d = OpenDesktopW(L"Default", 0, FALSE, DESKTOP_ENUMERATE | DESKTOP_READOBJECTS);
    if (d) {
        EnumDesktopWindows(d, tray_class_cb, (LPARAM)&found);
        CloseDesktop(d);
    }
    if (found)
        *seen_on = "the shell's desktop";
    return found;
}

/* Returns non-zero if the window appeared. A miss is not fatal: the notification is best effort. */
static int wait_for_tray_window(const char **seen_on)
{
    DWORD waited = 0;

    for (;;) {
        if (tray_window_present(seen_on))
            return 1;
        if (waited >= TRAYWND_WAIT_MS)
            return 0;
        Sleep(TRAYWND_POLL_MS);
        waited += TRAYWND_POLL_MS;
    }
}

/* Write the setting for one connection, by its id. Registry only, and deliberately so: nothing
 * about the value needs netman running or reachable. It is the connection's own setting, and
 * netman re-reads it on every GetProperties rather than caching it, so the write is complete on
 * its own and the notification below is a separate half that can fail without costing it. */
static int write_show_icon(const char *guid)
{
    char sub[256];
    HKEY k;
    DWORD one = 1;
    LONG r;

    connection_key_path(guid, sub, sizeof sub);
    /* Opened, never created. An absent key means the class installer has not finished with this
     * connection, and writing our own would put the value somewhere netman does not read. */
    r = RegOpenKeyExA(HKEY_LOCAL_MACHINE, sub, 0, KEY_SET_VALUE, &k);
    if (r != ERROR_SUCCESS) {
        say("  %s: no Connection subkey (%ld) - the class installer has not written one",
            guid, (long)r);
        return 1;
    }
    r = RegSetValueExA(k, "ShowIcon", 0, REG_DWORD, (const BYTE *)&one, sizeof one);
    RegCloseKey(k);
    if (r != ERROR_SUCCESS) {
        say("  %s: ShowIcon could not be written (%ld)", guid, (long)r);
        return 1;
    }
    say("  %s: ShowIcon = 1", guid);
    return 0;
}

/* Every connection the Net class installer has written a key for, with the setting applied.
 * Returns how many were written; *failed is set if any candidate could not be. */
static int set_all_show_icons(int *failed)
{
    static const char *kConnClass =
        "SYSTEM\\CurrentControlSet\\Control\\Network"
        "\\{4D36E972-E325-11CE-BFC1-08002BE10318}";
    HKEY cls;
    DWORD i;
    int n = 0;

    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, kConnClass, 0, KEY_READ, &cls) != ERROR_SUCCESS)
        return 0;
    for (i = 0;; i++) {
        char guid[64];
        DWORD len = sizeof guid;
        LONG r = RegEnumKeyExA(cls, i, guid, &len, NULL, NULL, NULL, NULL);

        /* ERROR_MORE_DATA is about this one index — a name too long for the buffer — so the walk
         * goes on; anything else is about the key or the handle and would repeat forever. */
        if (r == ERROR_MORE_DATA)
            continue;
        if (r != ERROR_SUCCESS)
            break;
        /* Descriptions and other bookkeeping live here too; only a GUID-shaped name is a
         * candidate connection. */
        if (guid[0] != '{')
            continue;
        if (write_show_icon(guid) == 0)
            n++;
        else
            *failed = 1;
    }
    RegCloseKey(cls);
    return n;
}

/* Tell whoever is already displaying a connection that it changed, and report what netman now
 * says about it. Returns non-zero if the notification was actually delivered.
 *
 * This is the half that needs netman, and it is best effort: a notification delivered before the
 * notification area's own object exists reaches nobody, and nothing retries it. That is not a
 * failure of the setting, which is already written by the time this runs — it costs the icon
 * until something else makes the tray look again.
 *
 * The re-read is not decoration. NCCF_SHOW_ICON is netman's own view of the value, so printing
 * it turns "we wrote something" into "netman agrees" — a distinction a boot has to be able to
 * make on its own, since the icon appearing is the only other evidence and it is a photograph
 * away. */
static int notify_one(pehw_INetConnectionRefresh *refresh, INetConnection *c)
{
    NETCON_PROPERTIES *p = NULL;
    HRESULT hr;
    char nbuf[256];
    int told = 0;

    /* The connection object itself, not its id — see the interface declaration above. */
    if (refresh) {
        hr = refresh->lpVtbl->ConnectionModified(refresh, c);
        if (FAILED(hr))
            say("  ConnectionModified failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
        else
            told = 1;
    }
    hr = INetConnection_GetProperties(c, &p);
    if (!p) {
        /* The same three cases report_netman keeps apart, and for the same reason:
         * S_OBJECT_NO_LONGER_VALID is a *success* code (0x00000002) meaning the connection went
         * away mid-walk, so printing it as a failure names an HRESULT hresult_name cannot look
         * up, for something that is not a fault. */
        if (hr == S_OBJECT_NO_LONGER_VALID)
            say("  (a connection that went away during the walk)");
        else if (SUCCEEDED(hr))
            say("  (a connection whose GetProperties returned nothing, 0x%08lX %s)",
                (unsigned long)hr, hresult_name(hr));
        else
            say("  (a connection whose GetProperties failed 0x%08lX %s)",
                (unsigned long)hr, hresult_name(hr));
        return told;
    }
    say("  \"%s\": character 0x%08lX%s", wide_str(p->pszwName, nbuf, sizeof nbuf),
        (unsigned long)p->dwCharacter,
        (p->dwCharacter & NCCF_SHOW_ICON) ? "  (NCCF_SHOW_ICON set)"
                                          : "  (NCCF_SHOW_ICON CLEAR)");
    CoTaskMemFree(p->pszwName);
    CoTaskMemFree(p->pszwDeviceName);
    CoTaskMemFree(p);
    return told;
}

/* Show the notification-area icon for every connection the machine has.
 *
 * Declared post-shell, because nothing earlier has either of the two things it needs: the Net
 * class installer creates the connection well after the call that produced it returns — measured
 * as still absent fifteen seconds into a pre-shell task ordered after the device pass — and
 * netman is not a registered class until the shell's self-registration pass, which runs after
 * that stage. So the pass is in two halves that fail independently: the registry write, which
 * needs only the key, and the notification, which needs netman and the notification area.
 *
 * Its own task rather than a step of `net` because the key it writes appears well after that pass
 * ends, and its own subcommand rather than part of `netconn` because `netconn` is a by-hand
 * report that instantiates third-party UI classes and must stay off the run list. */
static int cmd_trayicon(void)
{
    INetConnectionManager *mgr = NULL;
    pehw_INetConnectionRefresh *refresh = NULL;
    IEnumNetConnection *e = NULL;
    INetConnection *c = NULL;
    HRESULT hr;
    ULONG got = 0;
    DWORD waited = 0;
    const char *seen_on = "?";
    int n = 0, failed = 0, tray = 0, told = 0;

    SetUnhandledExceptionFilter(crash_filter);

    /* Wait for the class installer to write a connection's key, then set every one it wrote.
     * Polling the registry rather than asking netman, because netman is not reachable this early
     * and the key is the thing the setting belongs to either way.
     *
     * `failed` is cleared per attempt, not accumulated: the class installer creates a
     * connection's `{guid}` key before its `Connection` leaf, so an early poll legitimately finds
     * one it cannot write yet. Carrying that verdict forward would report the run as failed for
     * having been early — on exactly the boot the wait exists to serve. */
    for (;;) {
        failed = 0;
        n = set_all_show_icons(&failed);
        if (n)
            break;
        if (waited >= TRAYICON_WAIT_MS) {
            /* Not a failure of this pass: an image with no adapter bound has nothing to show an
             * icon for, and saying so is the whole report. */
            say("trayicon: no connection key after %lu s - nothing to show",
                (unsigned long)(TRAYICON_WAIT_MS / 1000));
            return 0;
        }
        Sleep(TRAYICON_POLL_MS);
        waited += TRAYICON_POLL_MS;
    }
    if (waited)
        say("trayicon: waited %lu s for a connection key", (unsigned long)(waited / 1000));

    /* The notification half. Everything below is best effort and reported as such rather than
     * returned as failure: the value is already written, so what is at stake is only whether the
     * icon appears now or at the tray's next look. Run by hand from a console this half is the
     * whole point — it is what makes the icon appear at once.
     *
     * The wait comes before the notification and not after, because a notification sent early is
     * not retried by anyone — it is delivered to whoever is listening at that instant and
     * reported as success either way. */
    tray = wait_for_tray_window(&seen_on);
    if (!tray)
        say("trayicon: no \"Connections Tray\" window on this or the shell's desktop after"
            " %lu s - notifying anyway", (unsigned long)(TRAYWND_WAIT_MS / 1000));

    /* Set them again before notifying. The loop above stops at the first connection with a key,
     * which is right — waiting out the whole budget on every boot to catch a second adapter that
     * usually is not there would cost every boot for a rare case. But a second adapter's key may
     * well have appeared during the tray wait, and re-walking costs one registry enumeration
     * with no added delay. Writes are idempotent, so this re-states the first as well and
     * returns the full count rather than a delta. */
    failed = 0;
    n = set_all_show_icons(&failed);

    hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    if (FAILED(hr)) {
        say("trayicon: %d written; CoInitializeEx 0x%08lX %s", n,
            (unsigned long)hr, hresult_name(hr));
        return failed;
    }
    hr = CoCreateInstance(&pehw_CLSID_ConnectionManager, NULL, CLSCTX_ALL,
                          &pehw_IID_INetConnectionManager, (void **)&mgr);
    if (FAILED(hr)) {
        CoUninitialize();
        say("trayicon: %d written, no notification (netman 0x%08lX %s) - the shell reads the"
            " value when it starts", n, (unsigned long)hr, hresult_name(hr));
        return failed;
    }
    hr = CoCreateInstance(&pehw_CLSID_ConnectionManager, NULL, CLSCTX_ALL,
                          &pehw_IID_INetConnectionRefresh, (void **)&refresh);
    if (FAILED(hr))
        refresh = NULL;

    hr = INetConnectionManager_EnumConnections(mgr, NCME_DEFAULT, &e);
    if (FAILED(hr) || !e) {
        say("trayicon: EnumConnections failed 0x%08lX %s", (unsigned long)hr, hresult_name(hr));
    } else {
        /* Same contract as report_netman's walk: test the pointer the call fills, not the
         * count, which an enumerator asked for one item may leave untouched. */
        for (;;) {
            c = NULL;
            if (IEnumNetConnection_Next(e, 1, &c, &got) != S_OK || !c)
                break;
            told += notify_one(refresh, c);
            INetConnection_Release(c);
        }
        IEnumNetConnection_Release(e);
    }

    if (refresh)
        refresh->lpVtbl->Release(refresh);
    INetConnectionManager_Release(mgr);
    CoUninitialize();
    /* Counted, not assumed: the summary says how many connections were actually told, because
     * having created the interface says nothing about a walk that enumerated none or a call that
     * failed — and this line is the whole of what the run list keeps. */
    say("trayicon: %d written, tray window %s%s, %d notified", n, tray ? "up on " : "NOT seen",
        tray ? seen_on : "", told);
    return failed;
}

static int cmd_ip(void)
{
    GETADAPTERSINFO get;
    IP_ADAPTER_INFO *info, *p;
    ULONG cb = 0;
    DWORD e;
    int n = 0;

    if (!g_iphlpapi)
        g_iphlpapi = LoadLibraryA("iphlpapi.dll");
    if (!g_iphlpapi) {
        say("iphlpapi.dll did not load (error %lu)", (unsigned long)GetLastError());
        return 1;
    }
    get = (GETADAPTERSINFO)(void(*)(void))GetProcAddress(g_iphlpapi, "GetAdaptersInfo");
    if (!get) {
        say("iphlpapi.dll exports no GetAdaptersInfo");
        return 1;
    }

    e = get(NULL, &cb);
    if (e == ERROR_NO_DATA) {
        /* Report the code, not a conclusion drawn from it: ERROR_NO_DATA is "no adapter
         * information", which is not by itself proof that nothing is bound. The two calls
         * below are what actually distinguish the cases. */
        say("ip: GetAdaptersInfo -> ERROR_NO_DATA (no adapter information)");
        ip_interfaces();
        return 1;
    }
    if (e != ERROR_BUFFER_OVERFLOW && e != ERROR_SUCCESS) {
        say("ip: GetAdaptersInfo sizing failed (error %lu)", (unsigned long)e);
        return 1;
    }
    info = (IP_ADAPTER_INFO *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, cb);
    if (!info) {
        say("ip: out of memory");
        return 1;
    }
    e = get(info, &cb);
    if (e != ERROR_SUCCESS) {
        say("ip: GetAdaptersInfo failed (error %lu)", (unsigned long)e);
        HeapFree(GetProcessHeap(), 0, info);
        return 1;
    }
    /* Walk a separate cursor: the list head is also the allocation, so advancing `info` itself
     * would lose the only pointer that can be freed. */
    for (p = info; p; p = p->Next, n++) {
        IP_ADDR_STRING *a;

        say("[%d] %s", n, p->Description);
        say("    adapter %s  type %lu  dhcp %s",
            p->AdapterName, (unsigned long)p->Type, p->DhcpEnabled ? "yes" : "no");
        for (a = &p->IpAddressList; a; a = a->Next)
            say("    ip %s  mask %s", a->IpAddress.String, a->IpMask.String);
        if (p->DhcpEnabled)
            say("    dhcp server %s", p->DhcpServer.IpAddress.String);
        for (a = &p->GatewayList; a; a = a->Next)
            if (a->IpAddress.String[0])
                say("    gateway %s", a->IpAddress.String);
    }
    HeapFree(GetProcessHeap(), 0, info);
    say("ip: %d adapter(s) known to the IP layer", n);
    ip_interfaces();
    return n ? 0 : 1;
}

static void usage(void)
{
    printf("pehw - runtime hardware configuration for a PE\n\n"
           "  pehw net [id..]       install network components by INF id (default ms_tcpip)\n"
           "                        and start the services they declare\n"
           "  pehw devices [all]    install drivers for devices that need one ('all' = every\n"
           "                        device, including ones already working)\n"
           "  pehw start <name>..   start one service by hand and print the raw error\n"
           "  pehw trayicon         show the notification-area icon for every connection\n"
           "\n"
           "  pehw status           report what is bound, what is running, and why not\n"
           "  pehw service <name>.. report a service: SCM state, registry, account, ImagePath\n"
           "  pehw ip               what the IP layer itself has: adapters, addresses, DHCP\n"
           "  pehw netconn          the network connection from both ends: its registry entry\n"
           "                        and what netman enumerates (what the shell's UI talks to)\n"
           "\n  -v  print the per-device trace (hardware ids, candidates, skips)\n");
}

int main(int argc, char **argv)
{
    int i, force = 0;

    /* -v anywhere turns on the per-device trace; `devices all` re-installs even devices that
     * report themselves healthy, which is what you want when testing the pass itself. */
    for (i = 1; i < argc; i++) {
        if (lstrcmpiA(argv[i], "-v") == 0)
            g_verbose = 1;
        else if (lstrcmpiA(argv[i], "all") == 0)
            force = 1;
    }

    /* The two passes, then the read-only reports. `start` is also a subcommand: `net` uses it
     * internally to bring up what it installs, and by hand it is the only way to ask the SCM
     * for one named service and read the code it gives back. */
    if (argc >= 2 && lstrcmpiA(argv[1], "net") == 0)
        return cmd_net(argc, argv);
    if (argc >= 2 && lstrcmpiA(argv[1], "devices") == 0)
        return cmd_devices(force);
    if (argc >= 2 && lstrcmpiA(argv[1], "trayicon") == 0)
        return cmd_trayicon();
    if (argc >= 2 && lstrcmpiA(argv[1], "status") == 0)
        return cmd_status();
    if (argc >= 2 && lstrcmpiA(argv[1], "ip") == 0)
        return cmd_ip();
    if (argc >= 2 && lstrcmpiA(argv[1], "netconn") == 0)
        return cmd_netconn();
    if (argc >= 3 && lstrcmpiA(argv[1], "service") == 0)
        return cmd_service(argc, argv);
    if (argc >= 3 && lstrcmpiA(argv[1], "start") == 0)
        return cmd_start(argc, argv);
    usage();
    return 2;
}
