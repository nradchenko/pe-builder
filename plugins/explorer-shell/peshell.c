// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* peshell.c — the PE Explorer-shell launcher (the SYSTEM\Setup\CmdLine program). A /minint boot
 * has no Winlogon to bring up the desktop, so peshell does it by hand: profile setup, splash,
 * service wait, pre-shell startup tasks, shell/COM self-registration, Start-menu shortcuts,
 * ghost-CPL prune, then launch Explorer (post-shell tasks once its taskbar paints, relaunch on
 * death, fall back to cmd). See README.md for the full sequence and why. */
#define _WIN32_WINNT 0x0501
#define WINVER 0x0501
#include <windows.h>
#include <objbase.h>
#include <shlobj.h>
#include "startmenu.h"

typedef HRESULT (WINAPI *DLLINSTALL)(BOOL, LPCWSTR);
typedef HRESULT (WINAPI *DLLREGISTER)(void);

/* Bounds for the plugin-declared self-registration list (see self_register_declared). Both are
 * generous for a contract whose entries are bare DLL file names. */
#define SELFREG_MAX  16
#define SELFREG_NAME 64

static const char *kProfileDirs[] = {
    "", "\\Desktop", "\\Start Menu", "\\Start Menu\\Programs",
    /* Startup lives at the profile root, not under Programs, so the (empty) auto-run folder is
     * not shown as a submenu in All Programs; User Shell Folders\Startup points here. */
    "\\Startup",
    "\\Application Data", "\\My Documents", "\\Favorites", "\\Recent", "\\SendTo", "\\Templates",
    "\\Local Settings", "\\Local Settings\\Application Data", "\\Local Settings\\Temp",
    "\\Local Settings\\Temporary Internet Files", "\\Local Settings\\History", NULL
};

/* The name of the mutex that marks the primary shell instance — see the guard at the top of
 * WinMain. */
#define SHELL_MUTEX "pe-builder.peshell"

/* The NT device the ramdrive driver creates, and the volume label its FAT16 formatter writes.
 * Both identify the RAM volume; they are not equally trustworthy, which is why there are two.
 *
 * The device name is the object manager's, and only a driver can put a name there — so nothing
 * written to a disk can claim it. The label is ordinary data in the volume's boot sector, so
 * anything able to format a disk can wear it: `pebuild disk create --label "PE RAMDISK"` would
 * be enough, and an attached disk at C: sorts ahead of the RAM volume at R:. That is exactly
 * the failure the two-pass scan below exists to prevent, so the label alone cannot be what
 * decides it. See README.md § The profile volume. */
#define RAMDISK_DEVICE "\\Device\\RamDrive"
#define RAMDISK_LABEL  "PE RAMDISK"

/* Does this letter resolve to the ramdrive driver's own device? The identity nothing on a disk
 * can forge: QueryDosDevice reports what the object manager has the letter pointing at, and our
 * driver creates \Device\RamDrive (mountmgr attaches the letter to it — see ramdrive.c's
 * IOCTL_MOUNTDEV_QUERY_SUGGESTED_LINK_NAME). A real volume answers \Device\HarddiskVolumeN. */
static BOOL is_ramdisk_device(char d){
    const char name[3]={d,':',0}; /* no trailing backslash: QueryDosDevice wants "R:", not "R:\" */
    char target[MAX_PATH];
    if(!QueryDosDeviceA(name,target,sizeof(target))) return FALSE;
    return lstrcmpiA(target,RAMDISK_DEVICE)==0;
}

/* Is this the ramdrive volume, judged by what it says about itself? DRIVE_RAMDISK first for any
 * ramdisk that does report it (ours, a WDM disk through the storage stack, comes back
 * DRIVE_FIXED like a real disk would), then the volume label.
 *
 * Kept as a second opinion rather than the first: both signals here are ones a disk can present,
 * so this runs only after the device check has failed to find the RAM volume at all. */
static BOOL is_ramdisk(char d){
    char root[4]={d,':','\\',0};
    if(GetDriveTypeA(root)==DRIVE_RAMDISK) return TRUE;
    char label[MAX_PATH+1];
    if(!GetVolumeInformationA(root,label,sizeof(label),NULL,NULL,NULL,NULL,0)) return FALSE;
    /* The label is an 11-byte space-padded FAT field, and FastFAT hands it back trimmed —
     * measured, on the boot this fix was verified against. Trim anyway: an exact compare would
     * make the whole fix rest on that, and if it ever did not hold the profile would go quietly
     * back to the attached disk this exists to avoid, with only a "(fallback)" line to show it. */
    int n=lstrlenA(label);
    while(n>0 && label[n-1]==' ') label[--n]=0;
    return lstrcmpiA(label,RAMDISK_LABEL)==0;
}

static BOOL drive_writable(char d){
    char probe[MAX_PATH]; wsprintfA(probe,"%c:\\pe_wt.tmp",d);
    HANDLE h=CreateFileA(probe,GENERIC_WRITE,0,NULL,CREATE_ALWAYS,FILE_ATTRIBUTE_TEMPORARY|FILE_FLAG_DELETE_ON_CLOSE,NULL);
    if(h==INVALID_HANDLE_VALUE) return FALSE;
    CloseHandle(h); return TRUE;
}

/* Pick the volume the user profile lives on. Three passes, in order of how much the evidence
 * can be trusted: the ramdrive's own device, then a volume claiming to be it, then any writable
 * volume at all. Taking the first writable drive letter instead would hand the profile to an
 * attached scratch disk whenever one sorts ahead of the RAM volume — writing user data to a
 * disk the user never asked us to touch. The last pass keeps a boot with no ramdrive working.
 *
 * The device pass leads because it is the only one an attached disk cannot satisfy. The label
 * pass below it is what this scan used to open with, and it stays: if the device query ever
 * fails — a driver that stops answering mountmgr, a letter attached some other way — the RAM
 * volume is still found, and the debug line says which evidence decided it, so a boot that
 * quietly changed its mind is visible rather than silent. */
static char find_writable_drive(void){
    /* Suppress the hard-error dialogs the scan can raise, and restore the mode after: a drive
     * with no media or an unformatted one answers a probe with "There is no disk in drive X:",
     * which here would appear before the splash, on a desktop with no Winlogon and nobody to
     * dismiss it — the bring-up would simply stop on a black screen. Scoped to the scan rather
     * than set for the process, because child processes inherit the mode and explorer's own
     * dialogs are not ours to suppress. Pass 0 makes this worth handling: it probes every
     * letter before accepting any, where the old scan stopped at the first writable one. */
    UINT prev=SetErrorMode(SEM_FAILCRITICALERRORS|SEM_NOOPENFILEERRORBOX);
    char found=0;
    DWORD mask=GetLogicalDrives();
    for(int pass=0;pass<3 && !found;pass++){
        for(char d='C';d<='Z';d++){
            if(!(mask&(1u<<(d-'A')))) continue;
            char root[4]={d,':','\\',0}; UINT t=GetDriveTypeA(root);
            if(t!=DRIVE_FIXED && t!=DRIVE_REMOVABLE && t!=DRIVE_RAMDISK) continue;
            if(pass==0 && !is_ramdisk_device(d)) continue;
            if(pass==1 && !is_ramdisk(d)) continue;
            if(!drive_writable(d)) continue;
            /* Say which volume, and what identified it. Falling past the device pass is not
             * a failure on its own, but it is the condition under which a mislabelled disk
             * could win, so it must be visible; falling all the way to the last pass is the
             * failure this scan exists to prevent, and from inside a booted PE there is no
             * other sign of it — the profile simply is not where it should be. Goes to the
             * kernel debugger; see README.md § Diagnosing a boot that stops partway. */
            static const char *const by[]={"ramdisk","ramdisk by label","fallback"};
            char msg[80];
            wsprintfA(msg,"peshell: profile volume %c: (%s)\n",d,by[pass]);
            OutputDebugStringA(msg);
            if(pass==1){
                /* The RAM volume was found, but not by the evidence that should have found
                 * it. Say what the letter actually resolves to: without it this line reports
                 * a surprise and explains nothing, and diagnosing it costs another boot of a
                 * desktop image. Only ever printed on the path that is already unexpected. */
                const char dev[3]={d,':',0};
                char target[MAX_PATH], why[MAX_PATH+80];
                if(QueryDosDeviceA(dev,target,sizeof(target)))
                    wsprintfA(why,"peshell: %c: resolves to %s, not %s\n",d,target,RAMDISK_DEVICE);
                else
                    wsprintfA(why,"peshell: %c: has no NT device mapping (error %lu)\n",
                              d,GetLastError());
                OutputDebugStringA(why);
            }
            found=d; break;
        }
    }
    if(!found) OutputDebugStringA("peshell: no writable volume; running without a profile\n");
    SetErrorMode(prev);
    return found;
}

/* Block until the COM baseline the shell needs is reachable, then return. The fixed SCM
 * auto-starts the core services (Start=2); we must NOT StartService them (that blocks on the
 * SCM database lock while the boot auto-start sequence drains). Bounded so a stuck SCM can't
 * wedge the boot. The body explains why we gate on SCM reachability rather than polling each
 * service to SERVICE_RUNNING. */
static void wait_services(void){
    /* Ensure only the COM baseline is reachable before explorer: OpenSCManager binds to the SCM
     * over RpcSs, so a successful open means RpcSs's endpoint is up (~1s). Do NOT poll services
     * for SERVICE_RUNNING — they cold-start off the CD for ~2min and one never cleanly flips to
     * RUNNING, timing out the wait; the shell doesn't need them running (browsing worked at
     * ~135s AND the old browsing hang was system sounds, now fixed). They finish in the
     * background while the desktop comes up in ~15s. */
    for(int k=0;k<120;k++){          /* up to ~60s guard; normally returns in ~1s */
        SC_HANDLE scm=OpenSCManagerA(NULL,NULL,SC_MANAGER_CONNECT);
        if(scm){ CloseServiceHandle(scm); return; }
        Sleep(500);
    }
}

/* Name a self-registration failure instead of dropping it. Registration stays best-effort —
 * nothing here aborts the boot — but a component that never registers surfaces far from here,
 * as an applet that dies or a dialog that wedges, with nothing pointing back at this pass.
 * Goes to the kernel debugger, so the serialdebug plugin makes it readable; see README.md
 * § Diagnosing a boot that stops partway. ASCII only: this text reaches a console that may be in
 * any code page. */
static void reg_fail(const char *dll, const char *what, DWORD code){
    /* Sized off SELFREG_NAME, the bound on the longest name that reaches here, rather than off
     * today's worst case: wsprintfA does not truncate, and that constant is documented as
     * generous and as something to raise (see self_register_declared). Coupling them here means
     * widening it cannot silently turn this into a stack overrun. */
    char msg[SELFREG_NAME + 96];
    wsprintfA(msg,"peshell: self-register %s: %s (0x%08lx)\n",dll,what,code);
    OutputDebugStringA(msg);
}

/* Self-register one shell/COM DLL through its own DllInstall entry point. Best-effort.
 * NOTE: DllInstall's argument is DLL-specific — each one parses the string itself, and a value
 * it does not recognise is silently ignored, so this call is only as good as the arg. */
static void dll_install(const char *dll, const WCHAR *arg){
    HMODULE h=LoadLibraryA(dll);
    if(!h){ reg_fail(dll,"load failed",GetLastError()); return; }
    DLLINSTALL p=(DLLINSTALL)GetProcAddress(h,"DllInstall");
    /* The module is deliberately left loaded, as before — these are servers the shell goes on
     * to use, so report through the branch rather than returning early out of a held handle. */
    if(!p) reg_fail(dll,"no DllInstall entry point",GetLastError());
    else { HRESULT hr=p(TRUE,arg); if(FAILED(hr)) reg_fail(dll,"DllInstall failed",(DWORD)hr); }
}

/* Register a plain COM server's classes (DllRegisterServer) — the standard entry point, unlike
 * the shell DLLs above whose DllInstall takes a private argument. Best-effort. */
static void dll_register(const char *dll){
    HMODULE h=LoadLibraryA(dll);
    if(!h){ reg_fail(dll,"load failed",GetLastError()); return; }
    DLLREGISTER p=(DLLREGISTER)(void*)GetProcAddress(h,"DllRegisterServer");
    if(!p) reg_fail(dll,"no DllRegisterServer entry point",GetLastError());
    else { HRESULT hr=p(); if(FAILED(hr)) reg_fail(dll,"DllRegisterServer failed",(DWORD)hr); }
}

/* Register the COM servers plugins declare under HKLM\SOFTWARE\pe-builder\SelfRegister — one
 * value per DLL, the value name being the file. See README.md § Self-registration for the
 * contract and why this sits outside the fixed sequence below. */
static void self_register_declared(void){
    HKEY k;
    if(RegOpenKeyExA(HKEY_LOCAL_MACHINE,"SOFTWARE\\pe-builder\\SelfRegister",0,KEY_READ,&k)!=ERROR_SUCCESS) return;
    char dll[SELFREG_MAX][SELFREG_NAME]; int n=0;
    for(DWORD i=0;; i++){
        char name[SELFREG_NAME]; DWORD nl=sizeof name;
        LONG r=RegEnumValueA(k,i,name,&nl,NULL,NULL,NULL,NULL);
        if(r==ERROR_NO_MORE_ITEMS) break;
        /* A name too big for the buffer comes back ERROR_MORE_DATA — skip that one and keep
         * going, rather than abandon the rest (as the Start-menu walk does). */
        if(r!=ERROR_SUCCESS) continue;
        if(n==SELFREG_MAX){
            /* Say so rather than drop it quietly — the resulting failure surfaces far from here. */
            OutputDebugStringA("peshell: SelfRegister list truncated, raise SELFREG_MAX\n");
            break;
        }
        lstrcpynA(dll[n++],name,SELFREG_NAME);
    }
    RegCloseKey(k);
    for(int i=1;i<n;i++){                      /* insertion sort; n is a handful */
        char t[SELFREG_NAME]; lstrcpynA(t,dll[i],SELFREG_NAME);
        int j=i-1;
        while(j>=0 && lstrcmpiA(dll[j],t)>0){ lstrcpynA(dll[j+1],dll[j],SELFREG_NAME); j--; }
        lstrcpynA(dll[j+1],t,SELFREG_NAME);
    }
    for(int i=0;i<n;i++) dll_register(dll[i]);
}

/* The shell/COM self-registration pass (see README.md § Self-registration). Only the complete
 * sequence is validated, so keep inetcpl's DllInstall and ie4uinit even though they look
 * IE-specific. */
static void self_register_shell(void){
    OleInitialize(NULL);
    dll_install("shell32.dll",  L"I");
    dll_install("browseui.dll", L"I");
    dll_install("shdocvw.dll",  L"I");
    dll_install("inetcpl.cpl",  L"I");
    {   STARTUPINFOA si; PROCESS_INFORMATION pi;
        ZeroMemory(&si,sizeof si); si.cb=sizeof si; si.lpDesktop=(LPSTR)"WinSta0\\Default";
        char cl[32]; lstrcpynA(cl,"ie4uinit.exe",32);
        if(CreateProcessA(NULL,cl,NULL,NULL,FALSE,0,NULL,NULL,&si,&pi)){
            WaitForSingleObject(pi.hProcess,20000); CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
        }
    }
    dll_install("urlmon.dll",   L"i,HKLM");
    dll_install("shell32.dll",  L"U");
    /* Display Properties builds its pages by CLSID, and themeui is a self-registering COM server:
     * staged but inert until its classes exist, so the applet dies before it can show anything.
     * It needs DllRegisterServer, NOT the DllInstall pass above — themeui's DllInstall only
     * recognises "/UserInstall" and "/InstallVS:" and ignores anything else, so registering it
     * that way is a silent no-op. Done last so it cannot perturb the sequence above. */
    dll_register("themeui.dll");
    self_register_declared();   /* plugin-declared servers, after the validated sequence */
}

static BOOL run_and_wait(const char *cmdline){
    STARTUPINFOA si; PROCESS_INFORMATION pi;
    ZeroMemory(&si,sizeof si); si.cb=sizeof si; si.lpDesktop=(LPSTR)"WinSta0\\Default";
    char buf[MAX_PATH]; lstrcpynA(buf,cmdline,MAX_PATH);
    if(!CreateProcessA(NULL,buf,NULL,NULL,FALSE,0,NULL,NULL,&si,&pi)) return FALSE;
    WaitForSingleObject(pi.hProcess,INFINITE);
    CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
    return TRUE;
}

/* Remove ghost Control Panel folders — namespace extensions shell32 registers at self-reg
 * time whose backing handler DLL we don't ship (they render as nameless empty folders).
 * Generic: drop any ControlPanel\NameSpace entry whose CLSID InprocServer32 DLL won't load;
 * entries with no InprocServer32 (filesystem folders like Administrative Tools) are kept. */
static void prune_ghost_cpl(void){
    static const char *NS =
        "SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Explorer\\ControlPanel\\NameSpace";
    HKEY ns; char dead[32][80]; int nd=0; char clsid[80]; DWORD i=0;
    if(RegOpenKeyExA(HKEY_LOCAL_MACHINE, NS, 0, KEY_READ, &ns)!=ERROR_SUCCESS) return;
    while(nd<32 && RegEnumKeyA(ns, i++, clsid, sizeof clsid)==ERROR_SUCCESS){
        if(clsid[0]!='{') continue;                     /* string-named .cpl refs: leave */
        /* Reserve the last byte and drop the read on failure/ERROR_MORE_DATA so the buffer
         * (zero-initialized) always stays NUL-terminated for the string checks below. */
        char dll[MAX_PATH]=""; DWORD len=sizeof dll-1; char cp[200];
        wsprintfA(cp,"CLSID\\%s\\InprocServer32",clsid); HKEY ips;
        if(RegOpenKeyExA(HKEY_CLASSES_ROOT,cp,0,KEY_READ,&ips)==ERROR_SUCCESS){
            if(RegQueryValueExA(ips,NULL,NULL,NULL,(LPBYTE)dll,&len)!=ERROR_SUCCESS) dll[0]=0;
            RegCloseKey(ips); }
        char name[128]=""; DWORD nl=sizeof name-1; char ck[200]; wsprintfA(ck,"CLSID\\%s",clsid);
        HKEY ch; if(RegOpenKeyExA(HKEY_CLASSES_ROOT,ck,0,KEY_READ,&ch)==ERROR_SUCCESS){
            if(RegQueryValueExA(ch,NULL,NULL,NULL,(LPBYTE)name,&nl)!=ERROR_SUCCESS) name[0]=0;
            RegCloseKey(ch); }
        int ghost = (name[0]==0 && dll[0]==0);          /* unregistered: no name, no server */
        if(!ghost && dll[0]){                            /* or a server DLL that won't load */
            /* Load it the way the shell's CoCreateInstance will — a full load that resolves
             * the import chain. A data-file mapping would succeed for any present PE even when
             * its dependency DLLs are absent, under-detecting a staged-but-broken handler; a
             * real load fails exactly where the shell would, so the ghost is pruned. Freed
             * immediately (and it's mostly shell32/shdocvw, already resident). */
            char exp[MAX_PATH]; ExpandEnvironmentStringsA(dll,exp,MAX_PATH);
            HMODULE h=LoadLibraryExA(exp,NULL,0);
            if(h) FreeLibrary(h); else ghost=1;
        }
        if(ghost) lstrcpynA(dead[nd++],clsid,80);
    }
    RegCloseKey(ns);
    if(nd && RegOpenKeyExA(HKEY_LOCAL_MACHINE, NS, 0, KEY_WRITE, &ns)==ERROR_SUCCESS){
        for(int k=0;k<nd;k++) RegDeleteKeyA(ns,dead[k]);
        RegCloseKey(ns);
    }
}

/* The "Please wait..." status splash (see README.md § Splash). Runs on its own thread so it keeps
 * animating while the main thread works; the banner is loaded from the source's msgina.dll at
 * runtime (resource 101), the thin bar is drawn ourselves (no comctl6). */
#define GINA_BANNER_BITMAP 101     /* msgina.dll: the workstation branding banner */
#define GINA_BANNER_SERVER 130     /* the Standard Edition one; see README.md § Which banner */
static HWND g_splash=NULL;
static char g_status[128]="Please wait...";
static HANDLE g_splashReady=NULL;
static HBITMAP g_banner=NULL;
static int g_bw=413, g_bh=88;      /* banner size (from the bitmap, or the fallback default) */
static int g_marquee=0;
#define WM_SPLASH_UPDATE (WM_APP+1)

/* Load the banner bitmap from the source's msgina.dll (as a data file — resources only, no import
 * resolution). LR_CREATEDIBSECTION copies it into a standalone bitmap so the module can be freed.
 *
 * Server media leaves 101 as the workstation artwork and brands its own banner at another
 * resource, so which one to ask for follows the product — see README.md § Which banner. The
 * product resolves to the ProductType the build reads from the source, so a wrong value there
 * surfaces here as the wrong logo rather than as anything louder. */
static HBITMAP load_gina_banner(void){
    HMODULE m=LoadLibraryExA("msgina.dll",NULL,LOAD_LIBRARY_AS_DATAFILE);
    if(!m) return NULL;
    HBITMAP b=NULL;
    OSVERSIONINFOEXA vi; ZeroMemory(&vi,sizeof vi); vi.dwOSVersionInfoSize=sizeof vi;
    if(GetVersionExA((OSVERSIONINFOA*)&vi) && vi.wProductType!=VER_NT_WORKSTATION)
        b=(HBITMAP)LoadImageA(m,MAKEINTRESOURCEA(GINA_BANNER_SERVER),IMAGE_BITMAP,0,0,LR_CREATEDIBSECTION);
    /* Fall back to the workstation banner: it is the one every source has. */
    if(!b)
        b=(HBITMAP)LoadImageA(m,MAKEINTRESOURCEA(GINA_BANNER_BITMAP),IMAGE_BITMAP,0,0,LR_CREATEDIBSECTION);
    FreeLibrary(m);
    return b;
}

/* Blue<->orange gradient for column i — the authentic XP "please wait" gas-gauge colours. The
 * colour sweeps from BLUE at the ends of each period to ORANGE in the middle (a triangle wave);
 * scrolled by g_marquee it reads as a running blue/orange bar. One full period spans the whole
 * bar (the banner width g_bw ~ 413px), matching the single broad sweep in the reference — a fixed
 * small period packs in too many humps. */
static COLORREF blueorange(int i){
    static const int BLUE[3]={49,106,197}, ORANGE[3]={245,160,50};
    int period=g_bw, half=period/2;
    int p=((i%period)+period)%period;                    /* position within one period */
    int t=(p<half ? p : period-1-p)*255/(half>1?half-1:1);   /* blend: 0 at the ends, 255 mid */
    int c[3];
    for(int k=0;k<3;k++) c[k]=BLUE[k]+(ORANGE[k]-BLUE[k])*t/255;
    return RGB(c[0],c[1],c[2]);
}

static void paint_splash(HDC dc,RECT rc){
    HBRUSH face=CreateSolidBrush(GetSysColor(COLOR_3DFACE));
    FillRect(dc,&rc,face); DeleteObject(face);

    /* Banner across the top: the source's own branding bitmap, or a blue fallback with a title. */
    if(g_banner){
        HDC md=CreateCompatibleDC(dc); HGDIOBJ ob=SelectObject(md,g_banner);
        BitBlt(dc,0,0,g_bw,g_bh,md,0,0,SRCCOPY);
        SelectObject(md,ob); DeleteDC(md);
    } else {
        RECT b={0,0,g_bw,g_bh}; HBRUSH bl=CreateSolidBrush(RGB(0,78,152)); FillRect(dc,&b,bl); DeleteObject(bl);
        SetBkMode(dc,TRANSPARENT); SetTextColor(dc,RGB(255,255,255));
        HFONT big=CreateFontA(28,0,0,0,FW_BOLD,0,0,0,0,0,0,ANTIALIASED_QUALITY,0,"Tahoma");
        HGDIOBJ of=SelectObject(dc,big);
        RECT t={0,0,g_bw,g_bh}; DrawTextA(dc,"Windows PE",-1,&t,DT_CENTER|DT_VCENTER|DT_SINGLELINE);
        SelectObject(dc,of); DeleteObject(big);
    }

    /* Thin running bar — full width, flush directly under the banner (the msgina "gas gauge"):
     * each column takes its blue<->orange colour from the sweep above, offset by g_marquee so the
     * bar reads as scrolling sideways. */
    int by=g_bh, bh=6;
    for(int x=0;x<g_bw;x++){
        RECT c={x,by,x+1,by+bh};
        HBRUSH b=CreateSolidBrush(blueorange(x-g_marquee)); FillRect(dc,&c,b); DeleteObject(b);
    }
    RECT sep0={0,by-1,g_bw,by}, sep1={0,by+bh,g_bw,by+bh+1};
    HBRUSH dk=CreateSolidBrush(RGB(120,120,120)); FillRect(dc,&sep0,dk); FillRect(dc,&sep1,dk); DeleteObject(dk);

    /* Phase text, vertically centered in the gray area below the bar. */
    SetBkMode(dc,TRANSPARENT); SetTextColor(dc,RGB(0,0,0));
    HFONT sml=CreateFontA(14,0,0,0,FW_NORMAL,0,0,0,0,0,0,ANTIALIASED_QUALITY,0,"Tahoma");
    HGDIOBJ of=SelectObject(dc,sml);
    RECT s={16,by+bh+1,g_bw-16,rc.bottom}; DrawTextA(dc,g_status,-1,&s,DT_LEFT|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS);
    SelectObject(dc,of); DeleteObject(sml);
}

static LRESULT CALLBACK SplashProc(HWND h,UINT m,WPARAM w,LPARAM l){
    if(m==WM_ERASEBKGND) return 1;                 /* fully painted via the back buffer below */
    if(m==WM_NCACTIVATE) return DefWindowProcA(h,m,TRUE,l);   /* always draw the caption active (blue) */
    if(m==WM_SYSCOMMAND && (w&0xFFF0)==SC_MOVE) return 0;     /* fixed in place — not movable */
    if(m==WM_PAINT){
        PAINTSTRUCT ps; HDC dc=BeginPaint(h,&ps);
        RECT rc; GetClientRect(h,&rc);
        HDC mem=CreateCompatibleDC(dc);
        HBITMAP bmp=CreateCompatibleBitmap(dc,rc.right,rc.bottom);
        HGDIOBJ ob=SelectObject(mem,bmp);
        paint_splash(mem,rc);
        BitBlt(dc,0,0,rc.right,rc.bottom,mem,0,0,SRCCOPY);
        SelectObject(mem,ob); DeleteObject(bmp); DeleteDC(mem);
        EndPaint(h,&ps); return 0;
    }
    if(m==WM_TIMER){ g_marquee+=7; InvalidateRect(h,NULL,FALSE); return 0; }
    if(m==WM_SPLASH_UPDATE){ InvalidateRect(h,NULL,FALSE); return 0; }
    if(m==WM_DESTROY){ KillTimer(h,1); PostQuitMessage(0); return 0; }
    return DefWindowProcA(h,m,w,l);
}
static DWORD WINAPI SplashThread(LPVOID p){
    (void)p;
    HDESK d=OpenDesktopA("Default",0,TRUE,GENERIC_ALL); if(d) SetThreadDesktop(d);
    g_banner=load_gina_banner();
    if(g_banner){ BITMAP bm; GetObject(g_banner,sizeof bm,&bm); g_bw=bm.bmWidth; g_bh=bm.bmHeight; }
    WNDCLASSA wc; ZeroMemory(&wc,sizeof wc);
    wc.lpfnWndProc=SplashProc; wc.hInstance=GetModuleHandleA(NULL);
    wc.hCursor=LoadCursorA(NULL,IDC_APPSTARTING);
    wc.hbrBackground=(HBRUSH)(COLOR_3DFACE+1);
    wc.lpszClassName="peshell_splash";
    RegisterClassA(&wc);
    int sw=GetSystemMetrics(SM_CXSCREEN), sh=GetSystemMetrics(SM_CYSCREEN);
    DWORD style=WS_POPUP|WS_CAPTION;           /* "Please wait..." title bar + border, no sysmenu */
    RECT wr={0,0,g_bw,g_bh+36}; AdjustWindowRect(&wr,style,FALSE);
    int ww=wr.right-wr.left, wh=wr.bottom-wr.top;
    /* WS_EX_TOOLWINDOW keeps the splash out of the taskbar: it is still on screen when explorer
     * starts, and explorer gives every ordinary top-level window a button as it enumerates them —
     * so without this a button for it flashes up and vanishes as the splash closes. */
    g_splash=CreateWindowExA(WS_EX_TOPMOST|WS_EX_TOOLWINDOW,"peshell_splash","Please wait...",style,
        (sw-ww)/2,(sh-wh)/2,ww,wh,NULL,NULL,wc.hInstance,NULL);
    if(g_splash){
        ShowWindow(g_splash,SW_SHOW); UpdateWindow(g_splash);
        SetForegroundWindow(g_splash);   /* be the active window so the caption paints blue */
        SetTimer(g_splash,1,40,NULL);
        SetCursorPos(sw-1,sh-1);   /* shove the centered boot cursor off the splash text */
    }
    if(g_splashReady) SetEvent(g_splashReady);
    MSG msg; while(GetMessageA(&msg,NULL,0,0)>0){ TranslateMessage(&msg); DispatchMessageA(&msg); }
    return 0;
}
static void splash_start(void){
    g_splashReady=CreateEventA(NULL,TRUE,FALSE,NULL);
    HANDLE th=CreateThread(NULL,0,SplashThread,NULL,0,NULL); if(th) CloseHandle(th);
    if(g_splashReady) WaitForSingleObject(g_splashReady,4000);
}
/* Every stage transition also goes to the kernel debugger, so a serialdebug boot reads as a
 * timeline of how far the shell bring-up got. This is the one place stages are announced, so
 * logging here keeps the trace complete as stages are added, and a boot that stops partway
 * names the stage it stopped in rather than going quiet. ASCII only: the debugger output may
 * be read on a console in any code page. */
static void splash_status(const char *msg){
    lstrcpynA(g_status,msg,sizeof g_status);
    /* Format the truncated copy, never the caller's string: perun_progress passes a 320-byte
     * buffer (a task label, or a name up to the contract's 255 characters — and under a
     * double-byte code page each character may convert to two), while wsprintfA does not bound
     * its output. Sized off g_status so the two cannot drift apart. */
    char line[sizeof g_status + 24];
    wsprintfA(line,"peshell: stage: %s\n",g_status);
    OutputDebugStringA(line);
    if(g_splash) PostMessageA(g_splash,WM_SPLASH_UPDATE,0,0);
}
static void splash_close(void){ if(g_splash){ PostMessageA(g_splash,WM_CLOSE,0,0); g_splash=NULL; } }

/* --- Startup run list --------------------------------------------------------------------
 * Walk the one-shot task list declared under HKLM\SOFTWARE\pe-builder\Startup — device and
 * network configuration a PE has to perform at runtime, since it runs none of the setup passes
 * Windows would have. The contract and the runner belong to the startup plugin; see its README.
 *
 * Loaded at runtime rather than linked. explorer-shell deliberately does not depend on startup,
 * so a desktop build that declares no tasks carries neither perun.dll nor a dangling import; the
 * plugin that needs tasks depends on startup, and then this finds it. That is the same shape as
 * the console path, where base's startnet.cmd guards the call on the file existing.
 *
 * No task runs in this process: perun reads the registry and CreateProcesses, so setupapi and
 * INetCfg never enter the shell's address space. */
#define PERUN_BEGIN (-1)
#define PERUN_OK      0

/* cdecl, matching perun.h — these must NOT be WINAPI. A stdcall/cdecl mismatch on i386 leaves
 * the stack unbalanced on every call and is silent until it is not. */
typedef void (*PERUN_PROGRESS)(void *ctx,const WCHAR *task,const WCHAR *label,
                               int index,int count,int state,DWORD err);
typedef DWORD (*PERUN_RUNSTAGE)(const WCHAR *stage,PERUN_PROGRESS cb,void *ctx,int *failedRequired);

/* Put each task's label on the splash as it starts, so the wait is accounted for rather than
 * silent — the reason the walk is in-process at all. */
static void perun_progress(void *ctx,const WCHAR *task,const WCHAR *label,
                           int index,int count,int state,DWORD err){
    /* Sized against the contract, not against what a task is likely to be called: a task key
     * may be 255 characters. msg must stay comfortably larger than name — wsprintfA below is
     * unbounded, so a name that outgrew it would overrun the stack rather than truncate. */
    char msg[320];
    (void)ctx;(void)index;(void)count;(void)err;

    if(state==PERUN_BEGIN){
        if(WideCharToMultiByte(CP_ACP,0,label,-1,msg,sizeof msg,NULL,NULL)) splash_status(msg);
        return;
    }
    if(state!=PERUN_OK){
        char name[256];
        /* A failed task never costs the boot; it is reported here and in its status key. Fall
         * back rather than return: the splash is the only surface this failure has, so losing
         * the name must not cost the whole notice. */
        if(!WideCharToMultiByte(CP_ACP,0,task,-1,name,sizeof name,NULL,NULL))
            lstrcpynA(name,"(unnamed)",sizeof name);
        wsprintfA(msg,"Startup task '%s' did not complete",name);
        splash_status(msg);
        if(g_splash) Sleep(1500);   /* long enough to read before the next label lands */
    }
}

static BOOL g_postShellDone=FALSE;   /* post-shell is one-shot across Explorer relaunches */

static void run_startup_stage(const WCHAR *stage){
    HMODULE m=LoadLibraryA("perun.dll");
    if(!m){                             /* the startup plugin is not in this build */
        OutputDebugStringA("peshell: startup stage skipped, no perun.dll\n");
        return;
    }
    PERUN_RUNSTAGE run=(PERUN_RUNSTAGE)(void(*)(void))GetProcAddress(m,"PerunRunStage");
    /* Announce the stage only once it can actually run, and name the missing export the way the
     * self-registration paths name theirs. peshell and the startup plugin are decoupled, so a
     * stale perun.dll is possible by design — reporting "stage started" and then nothing would
     * be indistinguishable from a stage that ran empty or hung, which is what this trace exists
     * to tell apart. */
    if(!run){
        OutputDebugStringA("peshell: startup stage skipped, perun.dll has no PerunRunStage\n");
    } else {
        /* Both callers pass a compile-time literal ("pre-shell"/"post-shell"), so the only
         * growth room needed is for a longer stage name; 96 leaves ~70 characters of it. */
        char line[96];
        wsprintfA(line,"peshell: startup stage %ls\n",stage);
        OutputDebugStringA(line);
        /* The required-failure count is deliberately ignored: the callback has already put every
         * failure on the splash and the runner has written each verdict to its status key, and a
         * failed task must not change what the boot does next. */
        int failed=0; run(stage,perun_progress,NULL,&failed);
    }
    FreeLibrary(m);
}

/* --- Visual style -----------------------------------------------------------------------
 * Apply the visual style the registry selects, the way Windows does at logon: there, winlogon
 * loads msgina.dll, which delay-loads SHSVCS.dll — the Themes service's *client* side — and drives
 * the apply before starting the shell. peshell stands in for winlogon here and does the same.
 *
 * Why this is peshell's job and not the Themes service's, and why it must be done by something
 * long-lived (both read out of the binaries, not assumed):
 *   - The service cannot apply a style itself. shsvcs delay-imports uxtheme ordinals 20-33/63/66
 *     but NOT #34, the RegisterUserApiHook path: the theme hook is always installed by the client,
 *     in the client's own process.
 *   - The hook lives exactly as long as that client's \ThemeApiPort connection — shsvcs imports
 *     UnregisterUserApiHook and drops the hook when the client goes. peshell runs for the life of
 *     the shell, so it can hold it; a helper that applied the style and exited would have it torn
 *     down a second later.
 *
 * The client API, as read from shsvcs.dll itself (msgina drives the same ordinals, which is why it
 * imports 1-6 rather than just the apply):
 *   Ordinal_2(dwTimeoutMs) -> 0 ok, 0x102 (WAIT_TIMEOUT) on failure. Waits up to dwTimeoutMs for
 *       the Themes service to reach RUNNING (it opens the SCM and polls QueryServiceStatus every
 *       50ms itself, so we need no wait of our own), connects this process's \ThemeApiPort client,
 *       allocates its session context, and turns the theme hooks on. This one call is the whole
 *       activation: IsThemeActive is already true when it returns.
 * Everything is resolved dynamically and every step is best-effort: with no visual-style plugin
 * enabled nothing is selected in the registry and this returns at the first check — and shsvcs.dll
 * isn't even staged. Such a PE keeps the classic look, exactly as before. */
typedef DWORD (WINAPI *THEMECONNECT)(DWORD);
typedef HRESULT (WINAPI *SETSYSVS)(LPCWSTR,LPCWSTR,LPCWSTR,DWORD);

/* Read one string value from an open key, expanding it if it is a REG_EXPAND_SZ path. Returns
 * FALSE unless `out` ends up holding the whole value: a caller that acts on a half-expanded path
 * would fail later and silently (a theme or wallpaper that just never appears), so refuse it
 * here instead. Used for every registry-declared path this file acts on. */
static BOOL reg_string(HKEY k,const WCHAR *name,WCHAR *out,DWORD count){
    WCHAR raw[MAX_PATH]; DWORD type=0,cb=sizeof raw;
    if(RegQueryValueExW(k,name,NULL,&type,(BYTE*)raw,&cb)!=ERROR_SUCCESS) return FALSE;
    /* Only a string is a string — a value of any other type would otherwise be copied out as
     * WCHARs and used as a path. */
    if(type!=REG_SZ && type!=REG_EXPAND_SZ) return FALSE;
    raw[MAX_PATH-1]=0;
    if(!raw[0]) return FALSE;
    if(type==REG_EXPAND_SZ){
        /* ExpandEnvironmentStringsW returns the REQUIRED size when the buffer is too small — a
         * non-zero value with `out` left truncated — so testing for 0 alone would accept it. */
        DWORD n=ExpandEnvironmentStringsW(raw,out,count);
        if(n==0 || n>count) return FALSE;
    }
    else lstrcpynW(out,raw,count);
    return out[0]!=0;
}

/* Wait (bounded) for the session subsystem before touching the theme stack. winsrv brings the
 * WinStation subsystem up only once termsrv signals this event, and the theme hooks live in that
 * subsystem, so applying before it is up would fail silently (a classic desktop, no error). Gate
 * on the event being SIGNALLED rather than on termsrv reporting RUNNING: it announces RUNNING
 * early and signals only at the end of the path that creates the WinStation port.
 *
 * Keep the bound SHORT. This runs on the boot path, before explorer is launched, so every second
 * waited is a second of no desktop — and the only thing a timeout costs is the theme. It has
 * measured 0ms on every boot (termsrv is long up by the time the shell registration finishes), so
 * this is headroom for a slower machine, not a fix for anything observed; a PE where the stack
 * never comes up must reach its desktop promptly, not minutes late. */
static BOOL wait_session_stack(int secs){
    for(int k=0;k<secs*2;k++){
        HANDLE ev=OpenEventA(SYNCHRONIZE,FALSE,"Global\\TermSrvReadyEvent");
        if(ev){
            DWORD r=WaitForSingleObject(ev,0);
            CloseHandle(ev);
            if(r==WAIT_OBJECT_0) return TRUE;
        }
        Sleep(500);
    }
    return FALSE;
}

static void apply_visual_style(void){
    HKEY k;
    if(RegOpenKeyExA(HKEY_CURRENT_USER,
                     "Software\\Microsoft\\Windows\\CurrentVersion\\ThemeManager",
                     0,KEY_READ,&k)!=ERROR_SUCCESS) return;
    /* DllName is a REG_EXPAND_SZ (%SystemRoot%\...): expand it, never rebuild the path by hand —
     * this PE boots from CD and its system drive is not C:. */
    WCHAR flag[8],style[MAX_PATH],color[64],size[64];
    BOOL on = reg_string(k,L"ThemeActive",flag,8) && flag[0]=='1'
              && reg_string(k,L"DllName",style,MAX_PATH);
    if(on){
        if(!reg_string(k,L"ColorName",color,64)) lstrcpyW(color,L"NormalColor");
        if(!reg_string(k,L"SizeName",size,64))   lstrcpyW(size,L"NormalSize");
    }
    RegCloseKey(k);
    if(!on) return;

    if(!wait_session_stack(10)) return;

    /* Both modules are released again on every path that gives up, and deliberately *not*
     * released on the one that succeeds — see the connection comment below. */
    HMODULE sh=LoadLibraryA("shsvcs.dll"); if(!sh) return;
    HMODULE ux=LoadLibraryA("uxtheme.dll"); if(!ux){ FreeLibrary(sh); return; }
    THEMECONNECT theme_connect=(THEMECONNECT)GetProcAddress(sh,(LPCSTR)2);
    SETSYSVS set_visual_style=(SETSYSVS)GetProcAddress(ux,(LPCSTR)65);
    if(!theme_connect||!set_visual_style){ FreeLibrary(ux); FreeLibrary(sh); return; }

    /* Connect as a theme client — this is what installs the hooks — and hold the connection for
     * the life of the process. Then select the style: SetSystemVisualStyle only checks that the
     * hooks are already on (it cannot install them) and fails with 0xd0000001 if they are not.
     * The timeout is Ordinal_2's own wait for the Themes service, and it is on the boot path too:
     * short for the same reason as wait_session_stack above — measured 0ms, and a timeout only
     * costs the theme. */
    if(theme_connect(15000)!=0){ FreeLibrary(ux); FreeLibrary(sh); return; }
    set_visual_style(style,color,size,0);
    /* sh and ux are intentionally left loaded: the theme connection installed above lives as
     * long as this process does, and unloading either would tear it down. */
}

/* Start Explorer, wait for its taskbar, run the tasks that need a live shell, then block until
 * it exits. Returns FALSE if it could not be started at all. Every launch goes through here, so
 * a desktop that comes back after Explorer dies is set up like the first. */
static BOOL launch_desktop(BOOL first){
    STARTUPINFOA si; PROCESS_INFORMATION pi;
    ZeroMemory(&si,sizeof si); si.cb=sizeof si; si.lpDesktop=(LPSTR)"WinSta0\\Default";
    char cl[64]; lstrcpynA(cl,"explorer.exe",64);
    if(!CreateProcessA(NULL,cl,NULL,NULL,FALSE,0,NULL,NULL,&si,&pi)) return FALSE;
    for(int k=0;k<600;k++){ if(FindWindowA("Shell_TrayWnd",NULL)) break; Sleep(300); }
    /* post-shell tasks want a live desktop, so they run here rather than after the call — this
     * function blocks until Explorer exits. Gated on having run rather than on `first`: a first
     * launch that fails returns above, and every retry passes first=FALSE, which would skip
     * these one-shot tasks for the whole boot on a desktop that came up on the second try.
     * Before splash_close, so their progress still has somewhere to show. */
    if(!g_postShellDone){ g_postShellDone=TRUE; run_startup_stage(L"post-shell"); }
    if(first) splash_close();
    WaitForSingleObject(pi.hProcess,INFINITE);
    CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
    return TRUE;
}

/* Widen an ANSI path for the shell APIs. Returns FALSE on failure so the caller skips the item
 * rather than handing a half-written buffer to a parser. */
static BOOL path_to_wide(const char *path, WCHAR *out, int count){
    return MultiByteToWideChar(CP_ACP,0,path,-1,out,count) != 0;
}

/* Create a .lnk shortcut to target at linkpath (COM must already be initialized). */
static void create_shortcut(const char *target, const char *linkpath){
    IShellLinkA *sl;
    if(CoCreateInstance(&CLSID_ShellLink,NULL,CLSCTX_INPROC_SERVER,&IID_IShellLinkA,(void**)&sl)!=S_OK) return;
    sl->lpVtbl->SetPath(sl,target);
    IPersistFile *pf;
    if(sl->lpVtbl->QueryInterface(sl,&IID_IPersistFile,(void**)&pf)==S_OK){
        WCHAR w[MAX_PATH];
        if(path_to_wide(linkpath,w,MAX_PATH)) pf->lpVtbl->Save(pf,w,TRUE);
        pf->lpVtbl->Release(pf);
    }
    sl->lpVtbl->Release(sl);
}

/* The Windows side of the Start-menu generator: the callbacks startmenu.c drives, plus the
 * enumeration that feeds them. The planner owns the layout and stays host-testable; everything
 * here touches the OS.
 *
 * The context carries the two anchors an entry can land under (All Programs, or the Start Menu
 * root) and collects root-anchored links so pin_top_level can pin them into the modern panel —
 * a side channel on the layout context, and a few KB of stack, both accepted at this scale. */
#define SM_MAXPINS 16
struct sm_ctx {
    char programs[MAX_PATH];
    char root[MAX_PATH];
    char pins[SM_MAXPINS][MAX_PATH];   /* root-anchored .lnk paths, in contract order */
    int  npins;
};

/* Read the target's version-resource FileDescription (localized per the source). The path is the
 * raw value data, so expand its %vars% first — this PE's system drive is not C:. Best-effort:
 * a target with no version resource returns 0 and the planner falls back to the name leaf. */
static int sm_filedesc(void *c, const char *rawtarget, char *out, int outsz){
    (void)c;
    char exp[MAX_PATH]; ExpandEnvironmentStringsA(rawtarget,exp,MAX_PATH);
    DWORD handle=0, sz=GetFileVersionInfoSizeA(exp,&handle);
    if(!sz) return 0;
    static BYTE buf[16384];             /* these applets' version blocks are a few KB; setup is single-threaded */
    if(sz>sizeof buf) return 0;
    if(!GetFileVersionInfoA(exp,0,sz,buf)) return 0;
    /* Read FileDescription for the binary's own first translation (language+codepage), so the
     * string comes back in the source's language rather than a guessed 040904b0. */
    struct { WORD lang, cp; } *tr=NULL; UINT trlen=0;
    if(!VerQueryValueA(buf,"\\VarFileInfo\\Translation",(void**)&tr,&trlen) || trlen<4 || !tr) return 0;
    char sub[80]; wsprintfA(sub,"\\StringFileInfo\\%04x%04x\\FileDescription",tr[0].lang,tr[0].cp);
    char *desc=NULL; UINT dl=0;
    if(!VerQueryValueA(buf,sub,(void**)&desc,&dl) || !desc || !desc[0]) return 0;
    lstrcpynA(out,desc,outsz);
    return out[0]!=0;
}

static const char *sm_base(struct sm_ctx *x, int at_root){ return at_root ? x->root : x->programs; }

/* Called parent-first, so one CreateDirectory suffices. The planner allows a deeper rel than a
 * MAX_PATH ANSI path holds, so bound the join and skip an over-long one rather than overflow. */
static void sm_ensure_dir(void *c, int at_root, const char *rel){
    struct sm_ctx *x=(struct sm_ctx*)c;
    const char *base=sm_base(x,at_root);
    if(lstrlenA(base)+1+lstrlenA(rel) >= MAX_PATH) return;
    char full[MAX_PATH]; wsprintfA(full,"%s\\%s",base,rel);
    CreateDirectoryA(full,NULL);
}

/* Drop the shortcut in its folder (rel="" -> straight under the anchor). COM is already
 * initialized (self_register_shell ran OleInitialize on this thread). Bounded like sm_ensure_dir:
 * base + rel + label + ".lnk" must fit MAX_PATH, else skip. */
static void sm_make_link(void *c, int at_root, const char *rel, const char *label, const char *rawtarget){
    struct sm_ctx *x=(struct sm_ctx*)c;
    const char *base=sm_base(x,at_root);
    int need=lstrlenA(base)+1+(rel[0]?lstrlenA(rel)+1:0)+lstrlenA(label)+4;   /* +"\..\.lnk" */
    if(need >= MAX_PATH) return;
    char full[MAX_PATH];
    if(rel[0]) wsprintfA(full,"%s\\%s\\%s.lnk",base,rel,label);
    else       wsprintfA(full,"%s\\%s.lnk",base,label);
    char exp[MAX_PATH]; ExpandEnvironmentStringsA(rawtarget,exp,MAX_PATH);
    create_shortcut(exp,full);
    /* Remember the top-level ones: the modern panel does not render Start Menu root shortcuts, so
     * these get pinned instead once the whole menu is built. */
    if(at_root && x->npins<SM_MAXPINS) lstrcpynA(x->pins[x->npins++],full,MAX_PATH);
}

/* --- Modern panel pinned list ------------------------------------------------------------------
 * The two-column panel does not render Start Menu root shortcuts, so the contract's top-level items
 * are pinned instead (see README.md § Modern Start menu). shell32's "Pin to Start menu" object —
 * the CLSID explorer-shell.inf already registers for the panel — exposes an undocumented
 * IStartMenuPin whose Modify(NULL,pidl) appends one item. It is registry I/O plus a change notify:
 * it reads no window handle, no data object and no explorer IPC, so peshell can pin before the shell
 * is started. Calling Modify (rather than hand-writing StartPage\Favorites) also lets shell32 author
 * the item bytes and store each one CSIDL-relative. */
/* Own copies: the SDK declares the CLSID but not this interface — its IStartMenuPinnedList is the
 * later, different one (RemoveFromList only). Named with the file's k-prefix to avoid colliding
 * with the SDK's CLSID declaration. */
static const GUID kCLSID_StartMenuPin =
    {0xA2A9545D,0xA0C2,0x42B4,{0x97,0x08,0xA0,0xB2,0xBA,0xDD,0x77,0xC8}};
static const GUID kIID_IStartMenuPin =
    {0xEC35E37A,0x6579,0x4F3C,{0x93,0xCD,0x6E,0x62,0xC4,0xEF,0x76,0x36}};

/* Only the slots up to Modify are declared — the object has three more (a change counter and two
 * unidentified) that we never call. */
typedef struct IStartMenuPinVtbl {
    HRESULT (STDMETHODCALLTYPE *QueryInterface)(void*, REFIID, void**);
    ULONG   (STDMETHODCALLTYPE *AddRef)(void*);
    ULONG   (STDMETHODCALLTYPE *Release)(void*);
    HRESULT (STDMETHODCALLTYPE *GetPinnedList)(void*, void**);
    HRESULT (STDMETHODCALLTYPE *Modify)(void*, LPCITEMIDLIST, LPCITEMIDLIST);   /* old, new */
} IStartMenuPinVtbl;
typedef struct { IStartMenuPinVtbl *lpVtbl; } IStartMenuPin;

/* Pin the collected top-level shortcuts, in contract order — the order they appear in the panel.
 * Best-effort: anything that won't resolve just leaves the desktop without that pin. The hive is
 * rebuilt from the baked copy every boot and this runs once per boot, so the list always starts
 * empty — hence no de-duplication. */
static void pin_top_level(const struct sm_ctx *x){
    if(!x->npins) return;
    IStartMenuPin *pin=NULL;
    if(CoCreateInstance(&kCLSID_StartMenuPin,NULL,CLSCTX_INPROC_SERVER,
                        &kIID_IStartMenuPin,(void**)&pin)!=S_OK || !pin) return;
    /* One Modify per item, and each one rewrites the whole list — the interface has no batch entry
     * point. Fine while SM_MAXPINS keeps the count small; it is the reason not to raise that cap. */
    for(int i=0;i<x->npins;i++){
        WCHAR w[MAX_PATH];
        if(!path_to_wide(x->pins[i],w,MAX_PATH)) continue;
        LPITEMIDLIST pidl=ILCreateFromPathW(w);
        if(!pidl) continue;
        pin->lpVtbl->Modify(pin,NULL,pidl);      /* (NULL,pidl) appends = pin */
        ILFree(pidl);
    }
    pin->lpVtbl->Release(pin);
}

/* Materialize the Start-menu items declared under HKLM\SOFTWARE\pe-builder\StartMenu — one value
 * per item (see README.md § Start-menu items for the contract). The INF plugins fill these keys,
 * so the menu is fully data-driven; the layout is decided by the pure planner in startmenu.c. */
static void setup_start_menu(char drive){
    if(!drive) return;
    struct sm_ctx x;
    x.npins=0;
    wsprintfA(x.root,"%c:\\profile\\Start Menu",drive);
    wsprintfA(x.programs,"%c:\\profile\\Start Menu\\Programs",drive);
    CreateDirectoryA(x.root,NULL);      /* both are in the kProfileDirs skeleton already; */
    CreateDirectoryA(x.programs,NULL);  /* belt-and-braces */
    HKEY k;
    if(RegOpenKeyExA(HKEY_LOCAL_MACHINE,"SOFTWARE\\pe-builder\\StartMenu",0,KEY_READ,&k)!=ERROR_SUCCESS) return;
    for(DWORD i=0;;i++){
        char name[256], data[MAX_PATH]; DWORD nl=sizeof name, dl=sizeof data, type;
        LONG r=RegEnumValueA(k,i,name,&nl,NULL,&type,(LPBYTE)data,&dl);
        if(r==ERROR_NO_MORE_ITEMS) break;
        /* A name/data too big for these buffers comes back ERROR_MORE_DATA — skip that one
         * value and keep going, rather than abandon the rest of the menu. */
        if(r!=ERROR_SUCCESS) continue;
        if(type!=REG_SZ && type!=REG_EXPAND_SZ) continue;
        sm_plan_entry(name,data,sm_filedesc,sm_ensure_dir,sm_make_link,&x);
    }
    RegCloseKey(k);
    pin_top_level(&x);      /* top-level items into the modern panel's pinned list */
}

/* Put an account picture where the modern Start panel looks for one: explorer builds the path as
 * <Common AppData>\Microsoft\User Account Pictures\<user>.bmp and draws nothing at all if the file
 * is absent. The bitmap to copy is whatever a plugin declared under HKLM\SOFTWARE\pe-builder as
 * UserPicture (see README.md § Account picture); no value = no picture, and this does nothing.
 *
 * The file name is the *token's* user, from GetUserName — not %USERNAME%, which is set to
 * "Default User" for the profile — so ask for it the same way explorer will rather than assume the
 * account. That lookup goes through the LSA, which is why this runs after the service wait. */
static void setup_user_picture(const char *allusers){
    char val[MAX_PATH]={0}, dir[MAX_PATH], src[MAX_PATH], dst[MAX_PATH], user[MAX_PATH];
    DWORD dl=sizeof val-1, type, n=sizeof user;
    HKEY k;
    if(RegOpenKeyExA(HKEY_LOCAL_MACHINE,"SOFTWARE\\pe-builder",0,KEY_READ,&k)!=ERROR_SUCCESS) return;
    LONG r=RegQueryValueExA(k,"UserPicture",NULL,&type,(LPBYTE)val,&dl);
    RegCloseKey(k);
    if(r!=ERROR_SUCCESS || (type!=REG_SZ && type!=REG_EXPAND_SZ) || !val[0]) return;
    /* Zero is failure; anything above the buffer size is the *required* size, and leaves src
     * untouched — both mean there is no path to copy from. */
    DWORD ex=ExpandEnvironmentStringsA(val,src,MAX_PATH);
    if(ex==0 || ex>MAX_PATH) return;
    if(!GetUserNameA(user,&n)) return;
    /* Create the folder ourselves: explorer asks for it with CSIDL_FLAG_CREATE, but not until it
     * reads the picture, which is after we have to have written one. */
    wsprintfA(dir,"%s\\Application Data\\Microsoft",allusers);
    CreateDirectoryA(dir,NULL);
    wsprintfA(dir,"%s\\Application Data\\Microsoft\\User Account Pictures",allusers);
    CreateDirectoryA(dir,NULL);
    wsprintfA(dst,"%s\\%s.bmp",dir,user);
    CopyFileA(src,dst,FALSE);
}

int WINAPI WinMain(HINSTANCE i,HINSTANCE p,LPSTR c,int s){
    (void)i;(void)p;(void)c;(void)s;
    OutputDebugStringA("peshell: start\n");

    /* Bring the shell up once and only once. The boot runs this as Setup\CmdLine, but nothing
     * stops it being started again from the desktop it produced — a shortcut, a stray double
     * click — and everything below assumes a session that has not been set up yet: it would
     * re-run the COM self-registration, rebuild the Start menu, reapply the visual style and
     * start a second Explorer over the live one. So the first instance claims a named mutex and
     * every later one says so and leaves. It says so in a message box because this is a GUI
     * program: started from a command prompt it owns no console and the prompt is already back
     * before it runs, and started from a shortcut there is no console at all — so a printed line
     * would be either misplaced or invisible, in the two ways it can be launched.
     *
     * The object exists only while a handle to it is open, so the claim lasts exactly as long as
     * this process stands for a desktop: a primary that died leaves nothing behind, and one that
     * gave up on the desktop drops the claim itself (see the cmd fallback at the end). Re-running
     * peshell in either state is a full bring-up — which is when redoing the sequence is the
     * point.
     *
     * A plain name, not Global\: a PE has the one interactive session, and the session namespace
     * needs no privilege. If the call fails outright we cannot tell, and carry on as before. */
    HANDLE primary=CreateMutexA(NULL,FALSE,SHELL_MUTEX);
    if(primary && GetLastError()==ERROR_ALREADY_EXISTS){
        OutputDebugStringA("peshell: a shell is already running; this instance does nothing\n");
        CloseHandle(primary);   /* the primary's handle is what keeps the object alive, not ours */
        /* Whoever launched this is looking at the desktop it would have built, so the box has
         * someone to dismiss it. MB_SETFOREGROUND because we are not the foreground process and
         * a box behind the window they started us from is the silence this exists to end. */
        MessageBoxA(NULL,"The desktop shell is already running.\n\n"
                         "peshell starts the desktop once, at boot. There is nothing for this "
                         "one to do.","pe-builder",
                    MB_OK|MB_ICONINFORMATION|MB_SETFOREGROUND);
        return 0;
    }

    char drive=find_writable_drive();
    char au[MAX_PATH]; au[0]=0;
    if(drive){
        char up[MAX_PATH]; wsprintfA(up,"%c:\\profile",drive);
        char pd[MAX_PATH];
        for(int k=0;kProfileDirs[k];k++){ wsprintfA(pd,"%s%s",up,kProfileDirs[k]); CreateDirectoryA(pd,NULL); }
        SetEnvironmentVariableA("USERPROFILE",up);
        SetEnvironmentVariableA("USERNAME","Default User");
        /* Point TEMP/TMP at the profile's temp dir (created by the kProfileDirs loop above).
         * explorer and its children inherit this environment, so apps have a writable temp.
         * The registry defines these too, but nothing in /minint propagates the registry
         * environment into the process tree (no winlogon/userinit), so set them here. */
        char tmp[MAX_PATH]; wsprintfA(tmp,"%s\\Local Settings\\Temp",up);
        SetEnvironmentVariableA("TEMP",tmp);
        SetEnvironmentVariableA("TMP",tmp);
        wsprintfA(au,"%c:\\All Users",drive);
        static const char *cd[]={"","\\Desktop","\\Start Menu","\\Start Menu\\Programs",
            "\\Startup","\\Application Data","\\Documents",NULL};   /* Startup off Programs — see kProfileDirs */
        for(int k=0;cd[k];k++){ char x[MAX_PATH]; wsprintfA(x,"%s%s",au,cd[k]); CreateDirectoryA(x,NULL); }
        SetEnvironmentVariableA("ALLUSERSPROFILE",au);
    }

    /* Switch to the interactive desktop first, then show the status window on it, so the ~15s
     * of shell loading isn't a silent black screen. */
    HDESK hd=OpenDesktopA("Default",0,TRUE,GENERIC_ALL);
    if(hd){ SetThreadDesktop(hd); SwitchDesktop(hd); }
    splash_start();

    splash_status("Starting services...");
    wait_services();

    /* One-shot startup tasks, before the shell. After wait_services deliberately: a device or
     * network install pass needs the service stack up to do anything. */
    run_startup_stage(L"pre-shell");

    splash_status("Registering shell components...");
    self_register_shell();

    splash_status("Setting up the Start menu...");
    setup_start_menu(drive);
    /* After wait_services, not with the rest of the profile setup: naming the file needs the
     * account name, and that lookup goes through the LSA — which is not reachable at the point
     * the profile directories are created. Still before explorer, which reads the picture once
     * when it builds the Start panel and caches it. */
    if(au[0]) setup_user_picture(au);

    splash_status("Preparing the Control Panel...");
    prune_ghost_cpl();      /* after self-reg: shell32 adds the CP namespace entries there */

    /* Apply the visual style BEFORE explorer starts, so the desktop paints themed from its first
     * frame instead of coming up classic and flipping. This is where a real Windows does it too:
     * winlogon's GINA applies the style during logon, before it launches the shell. */
    splash_status("Applying the visual style...");
    apply_visual_style();

    splash_status("Launching the desktop...");

    /* On the first launch the status window stays up until the taskbar (Shell_TrayWnd) actually
     * paints, then closes — the desktop is visible, so the splash has done its job. Every launch
     * goes through the same helper, so a desktop that comes back after Explorer dies is set up
     * the same way the first one was. */
    if(!launch_desktop(TRUE)) splash_close();
    for(int deaths=0; deaths<4; deaths++){
        /* "relaunching" rather than "explorer exited": the loop is also entered when the first
         * launch never started Explorer at all, and this trace exists to diagnose a boot that
         * stopped partway — asserting a state that did not happen would defeat that. */
        OutputDebugStringA("peshell: relaunching explorer\n");
        if(!launch_desktop(FALSE)) break;
        Sleep(500);
    }
    OutputDebugStringA("peshell: desktop gone, falling back to cmd.exe\n");
    /* Drop the primary claim before the prompt. This process outlives the desktop it brought up —
     * it sits here waiting on cmd — and running peshell from that prompt is the obvious way back.
     * Still holding the mutex would answer that with "the desktop shell is already running", which
     * is the one situation where it is not. */
    if(primary) CloseHandle(primary);
    run_and_wait("cmd.exe");
    return 0;
}
