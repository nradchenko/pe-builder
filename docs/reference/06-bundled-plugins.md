# 06 — Bundled Plugins (PE Builder 3.1.10a)

The following plugins ship inside PE Builder 3.1.10a's `plugin\` folder. Names are
the authoritative `[PEBuilder] Name=` values from each plugin's `.inf`.

**Availability** reflects what the original project noted about each plugin:

- **Included** — self-contained, ready to use.
- **Needs files** — the plugin `.inf` is included, but the user must supply the
  actual program files (download the application, or in some cases buy it) before
  the plugin will build.

Being shipped in the archive does **not** mean the third-party software is
included — for application plugins, only the integration `.inf` (and small
support files) is present.

## Core system & boot

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `bartpe` | BARTPE: Network Support | Core networking stack for the environment. | Included |
| `ramdisk` | RAMDisk [Nu2 Productions] | RAM-disk driver. Required for USB/RAM-disk boot and a dependency of XPE. | Included |
| `bootfix` | Boot Fix (Enabling "Press any key to boot from CD") | Adds `bootfix.bin` for the CD boot prompt. The canonical minimal plugin. | Included |
| `sermouse` | Serial Mouse | Serial mouse support. | Included |
| `keyboard` | Keyboard Layout | Keyboard layout selection (see its PluginHelp). | Included |
| `aspi` | Adaptec ASPI | ASPI layer for CD/optical access. | Included |
| `dcomlaunch` | RpcSS needs to launch DComLaunch Service first - SP2 only | Service-ordering fix for SP2 sources. | Included |
| `dospe` | Dos 16-bit support for WinPE | 16-bit DOS application support. | Included |
| `!custom` | Customize | User customization catch-all; the `!` prefix makes it load **first**. | Included |

## Shells & menus

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `nu2menu` | Nu2Menu | The default lightweight menu shell; plugins extend it via `[Append]` on `nu2menu.xml`. | Included |
| `zz5` | *(nu2shell)* | Alternative shell; supports boot **password protection** via its `Config=` configurator (`nu2shellcfg.exe`). The `zz` prefix loads it **last**. | Included |
| `autorun` | Startup Group | Runs programs automatically at startup. | Included |

## Recovery, diagnostics & disk

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `chkdsk` | Check Disk (chkdsk.exe) | Adds `chkdsk`. | Included |
| `bst5` | Bart's Stuff Test (free edition) | Media/drive stress test. | Included |
| `a43` | A43 File Management Utility | Lightweight file manager. | Included |
| `peinst` | BartPE Installer v2 | Installs BartPE onto a hard disk. | Included |
| `dskcmd` | Disk Commander v1.1 | Disk repair/recovery tool. | Needs files |
| `erd2002` | ERD Commander 2002 | Recovery environment (Winternals). | Needs files |
| `snapshot` | Drive SnapShot | Disk imaging (Tom Ehlert). | Needs files |
| `ghost8` | Symantec Ghost 8.0 | Disk imaging (commercial). | Needs files (purchase) |

## Networking & remote access

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `penetcfg` | PENETCFG: PE Network configurator (theTruth) | Interactive network configuration (Pierre Mounir). | Included |
| `mstsc` | Remote Desktop Client | Microsoft RDP client. | Needs files |
| `putty` | PuTTY | SSH/telnet client. | Needs files |
| `ultravnc` | UltraVNC | Remote control server/viewer. | Needs files |
| `StarWind` | StarWind iSCSI Server | iSCSI target (Rocket Division). | Needs files |

## Anti-malware

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `adawarese` | Ad-Aware SE | Anti-spyware scanner (Lavasoft). | Needs files |
| `mcafee` | McAfee VirusScan for Win32 | Command-line virus scanner. | Needs files |
| `stinger` | McAfee Stinger | Standalone virus removal tool. | Needs files |

## Applications & utilities

| Folder | Name | Purpose | Availability |
|--------|------|---------|--------------|
| `bginfo` | Background info (bginfo) | Renders system info onto the desktop. | Needs files |
| `irfanview` | IrfanView | Image viewer. | Needs files |
| `total commander` | Total Commander v6 | Dual-pane file manager. | Needs files |
| `deepburner` | Deep Burner Free | CD/DVD burning (Astonsoft). | Needs files |
| `nero burning rom` | Nero Burning Rom | CD/DVD burning (commercial). | Needs files (purchase) |

## Notes

- The nu2.nu website additionally linked to a large ecosystem of **externally
  hosted** plugins (Firefox, Total Commander, hex editors, NetWare client, and
  many recovery/imaging tools), plus commercial vendor-supplied plugins. Those
  are not part of the shipped archive.
- Sherpya's **Windows XPE** (the desktop-shell plugin) is *not* bundled with PE
  Builder; it is a separate download and is documented in
  [`05-case-study-xpe.md`](05-case-study-xpe.md).