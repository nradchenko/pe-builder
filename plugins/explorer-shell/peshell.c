// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* peshell.c — the PE Explorer-shell launcher (the SYSTEM\Setup\CmdLine program). A /minint boot
 * has no Winlogon to bring up the desktop, so peshell does it by hand: profile setup, splash,
 * service wait, shell/COM self-registration, Start-menu shortcuts, ghost-CPL prune, then launch
 * Explorer (relaunch on death, fall back to cmd). See README.md for the full sequence and why. */
#define _WIN32_WINNT 0x0501
#define WINVER 0x0501
#include <windows.h>
#include <objbase.h>
#include <shlobj.h>

typedef HRESULT (WINAPI *DLLINSTALL)(BOOL, LPCWSTR);

static const char *kProfileDirs[] = {
    "", "\\Desktop", "\\Start Menu", "\\Start Menu\\Programs", "\\Start Menu\\Programs\\Startup",
    "\\Application Data", "\\My Documents", "\\Favorites", "\\Recent", "\\SendTo", "\\Templates",
    "\\Local Settings", "\\Local Settings\\Application Data", "\\Local Settings\\Temp",
    "\\Local Settings\\Temporary Internet Files", "\\Local Settings\\History", NULL
};

static char find_writable_drive(void){
    DWORD mask=GetLogicalDrives();
    for(char d='C';d<='Z';d++){
        if(!(mask&(1u<<(d-'A')))) continue;
        char root[4]={d,':','\\',0}; UINT t=GetDriveTypeA(root);
        if(t!=DRIVE_FIXED && t!=DRIVE_REMOVABLE && t!=DRIVE_RAMDISK) continue;
        char probe[MAX_PATH]; wsprintfA(probe,"%c:\\pe_wt.tmp",d);
        HANDLE h=CreateFileA(probe,GENERIC_WRITE,0,NULL,CREATE_ALWAYS,FILE_ATTRIBUTE_TEMPORARY|FILE_FLAG_DELETE_ON_CLOSE,NULL);
        if(h!=INVALID_HANDLE_VALUE){ CloseHandle(h); return d; }
    }
    return 0;
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

/* Self-register one shell/COM DLL through its own DllInstall entry point. Best-effort. */
static void dll_install(const char *dll, const WCHAR *arg){
    HMODULE h=LoadLibraryA(dll); if(!h) return;
    DLLINSTALL p=(DLLINSTALL)GetProcAddress(h,"DllInstall");
    if(p) p(TRUE,arg);
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
#define GINA_BANNER_BITMAP 101     /* msgina.dll: the ~413x88 "Windows" branding banner */
static HWND g_splash=NULL;
static char g_status[128]="Please wait...";
static HANDLE g_splashReady=NULL;
static HBITMAP g_banner=NULL;
static int g_bw=413, g_bh=88;      /* banner size (from the bitmap, or the fallback default) */
static int g_marquee=0;
#define WM_SPLASH_UPDATE (WM_APP+1)

/* Load the banner bitmap from the source's msgina.dll (as a data file — resources only, no import
 * resolution). LR_CREATEDIBSECTION copies it into a standalone bitmap so the module can be freed. */
static HBITMAP load_gina_banner(void){
    HMODULE m=LoadLibraryExA("msgina.dll",NULL,LOAD_LIBRARY_AS_DATAFILE);
    if(!m) return NULL;
    HBITMAP b=(HBITMAP)LoadImageA(m,MAKEINTRESOURCEA(GINA_BANNER_BITMAP),IMAGE_BITMAP,0,0,LR_CREATEDIBSECTION);
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

    /* Thin running rainbow bar — full width, flush directly under the banner (the msgina "gas
     * gauge"): each column is a spectrum colour that scrolls sideways as g_marquee advances. */
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
    g_splash=CreateWindowExA(WS_EX_TOPMOST,"peshell_splash","Please wait...",style,
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
static void splash_status(const char *msg){
    lstrcpynA(g_status,msg,sizeof g_status);
    if(g_splash) PostMessageA(g_splash,WM_SPLASH_UPDATE,0,0);
}
static void splash_close(void){ if(g_splash){ PostMessageA(g_splash,WM_CLOSE,0,0); g_splash=NULL; } }

/* Create a .lnk shortcut to target at linkpath (COM must already be initialized). */
static void create_shortcut(const char *target, const char *linkpath){
    IShellLinkA *sl;
    if(CoCreateInstance(&CLSID_ShellLink,NULL,CLSCTX_INPROC_SERVER,&IID_IShellLinkA,(void**)&sl)!=S_OK) return;
    sl->lpVtbl->SetPath(sl,target);
    IPersistFile *pf;
    if(sl->lpVtbl->QueryInterface(sl,&IID_IPersistFile,(void**)&pf)==S_OK){
        WCHAR w[MAX_PATH]; MultiByteToWideChar(CP_ACP,0,linkpath,-1,w,MAX_PATH);
        pf->lpVtbl->Save(pf,w,TRUE);
        pf->lpVtbl->Release(pf);
    }
    sl->lpVtbl->Release(sl);
}

/* Materialize the Start-menu items declared under HKLM\SOFTWARE\pe-builder\StartMenu — one value
 * per item: the value name is the label, its data the (env-expandable) target path. The INF plugin
 * fills these keys, so the top-level menu is fully data-driven — nothing is hardcoded here. Each
 * becomes a shortcut in the Start Menu root, which also props the classic menu up to the height
 * shell32 needs before it draws the vertical side banner. */
static void setup_start_menu(char drive){
    if(!drive) return;
    HKEY k;
    if(RegOpenKeyExA(HKEY_LOCAL_MACHINE,"SOFTWARE\\pe-builder\\StartMenu",0,KEY_READ,&k)!=ERROR_SUCCESS) return;
    for(DWORD i=0;;i++){
        char label[128], data[MAX_PATH]; DWORD nl=sizeof label, dl=sizeof data, type;
        if(RegEnumValueA(k,i,label,&nl,NULL,&type,(LPBYTE)data,&dl)!=ERROR_SUCCESS) break;
        if(type!=REG_SZ && type!=REG_EXPAND_SZ) continue;
        char target[MAX_PATH]; ExpandEnvironmentStringsA(data,target,MAX_PATH);
        char lnk[MAX_PATH]; wsprintfA(lnk,"%c:\\profile\\Start Menu\\%s.lnk",drive,label);
        create_shortcut(target,lnk);
    }
    RegCloseKey(k);
}

int WINAPI WinMain(HINSTANCE i,HINSTANCE p,LPSTR c,int s){
    (void)i;(void)p;(void)c;(void)s;

    char drive=find_writable_drive();
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
        char au[MAX_PATH]; wsprintfA(au,"%c:\\All Users",drive);
        static const char *cd[]={"","\\Desktop","\\Start Menu","\\Start Menu\\Programs",
            "\\Start Menu\\Programs\\Startup","\\Application Data","\\Documents",NULL};
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

    splash_status("Registering shell components...");
    self_register_shell();

    splash_status("Setting up the Start menu...");
    setup_start_menu(drive);

    splash_status("Preparing the Control Panel...");
    prune_ghost_cpl();      /* after self-reg: shell32 adds the CP namespace entries there */

    splash_status("Launching the desktop...");

    /* First launch: keep the status window up until the taskbar (Shell_TrayWnd) actually
     * paints, then close it — the desktop is now visible, so the splash has done its job. */
    {
        STARTUPINFOA si; PROCESS_INFORMATION pi;
        ZeroMemory(&si,sizeof si); si.cb=sizeof si; si.lpDesktop=(LPSTR)"WinSta0\\Default";
        char cl[64]; lstrcpynA(cl,"explorer.exe",64);
        if(CreateProcessA(NULL,cl,NULL,NULL,FALSE,0,NULL,NULL,&si,&pi)){
            for(int k=0;k<600;k++){ if(FindWindowA("Shell_TrayWnd",NULL)) break; Sleep(300); }
            splash_close();
            WaitForSingleObject(pi.hProcess,INFINITE);
            CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
        } else splash_close();
    }
    for(int deaths=0; deaths<4; deaths++){
        if(!run_and_wait("explorer.exe")) break;
        Sleep(500);
    }
    run_and_wait("cmd.exe");
    return 0;
}
