# 03 — Plugin `.inf` File Format

A plugin is described by one (or more) `.inf` files that instruct PE Builder to
copy files, create directories, add registry entries, and patch text/INI files
into the build. `pebuilder.inf` (the base manifest) uses the same grammar.

This is the authoritative reference for the format, derived from PE Builder
3.1.10a's `pluginformat.htm` and cross-checked against the shipped plugins.

## File requirements

- The `.inf` must be a plain **ASCII** file (not Unicode).
- Lines beginning with `;` (semicolon) are **comments** and ignored.
- The file **must** contain a `[Version]` section with the signature line, so
  that the Windows setupapi INF functions can open it:

  ```ini
  [Version]
  Signature= "$Windows NT$"
  ```

- The file **must** contain a `[PEBuilder]` section.

> PE Builder parses plugins with the **Windows setupapi INF parser**
> (`SetupOpenInfFile`, `SetupGetStringField`, …), so the format *is* the standard
> setupapi INF format and all its semantics apply: comma-separated **fields**,
> double-quoted values (with `""` for an embedded quote), `%TOKEN%` substitution
> from a `[Strings]` section, and `\` line continuation. This is also why the
> `[Version]` signature is mandatory. See
> [`07-engine-internals.md`](07-engine-internals.md).

## Environment variables

The following variables may be used inside plugin files (case-sensitive), and are
expanded by PE Builder:

| Variable | Meaning |
|----------|---------|
| `@SourcePath@` | The Windows source path selected in PE Builder (the folder containing `i386`). |
| `@ProfilesDir@` | The `<PE>\Documents and Settings` folder. |
| `@Program@` | The `<PE>\PE Builder` folder. |
| `@PluginDir@` | The plugin's own directory. |
| `@PluginFile@` | The plugin's main `.inf` file path (used by `Config=`). |
| `@IsoFile@`, `@OutDir@`, `@Verbose@`, `@Build@`, `@Version@` | ISO filename, output dir, verbose flag, source build number, and PE Builder version, respectively. |

## Conditional sections (`.BuildNo`)

Most section names may be suffixed with a **build number** so the section applies
only when the source media matches:

| Build number | Source |
|--------------|--------|
| `2600` | Windows XP |
| `3790` | Windows Server 2003 |

```ini
[SourceDisksFiles.2600]
files\xp\sp2\ole32.dll=2,,1     ; used only for XP

[SourceDisksFiles.3790]
files\2k3\rtmgdr\ole32.dll=2,,1 ; used only for Server 2003
```

This mechanism is the backbone of multi-source support in plugins (XPE, for
example, uses `.2600` / `.3790` variants extensively).

---

## `[PEBuilder]`

Required. Describes the plugin to PE Builder and its plugin dialog.

| Key | Meaning |
|-----|---------|
| `Name` | The display name of the plugin. |
| `Enable` | Default enabled state: `0` = disabled, `1` = enabled. (This is the initial Yes/No shown in the plugin dialog.) |
| `Help` | Filename of a help document. PE Builder opens it with `ShellExecute()` (so it can be `.txt`, `.htm`, `.pdf`, `.doc`, or any registered type). |
| `Config` | Command line of a **configuration program** for the plugin, run by the plugin dialog's *Config* button. Behaviour is plugin-specific. |

Example (`bootfix` — the canonical minimal plugin):

```ini
[Version]
Signature= "$Windows NT$"

[PEBuilder]
Name="Boot Fix (Enabling ""Press any key to boot from CD"")"
Enable=0
Help="bootfix.htm"

[SourceDisksFiles]
bootfix.bin=1
```

Example `Config=` (from the `nu2shell` plugin) — launches a native configurator,
passing the plugin's own file:

```ini
[PEBuilder]
Config=nu2shellcfg.exe @PluginFile@
```

Note: embedded double-quotes inside a quoted value are written **doubled** (`""`),
as in the `bootfix` name above.

---

## `[WinntDirectories]`

Defines directories in the output tree and assigns them numeric IDs used by the
file-copy sections.

**Format:** `directoryID=dirname[, attribute]`

- `directoryID` — the directory number. In plugin files you may use the letters
  `a`–`z` for **dynamic** ID assignment (PE Builder allocates real numbers).
- `dirname` — the directory path; quote it if it contains spaces.
- `attribute` — a combinable bitfield:
  - `1` = always create this directory, even when empty.
  - `2` = create the directory from the **output root** (`\`), not inside `\i386`.
  - default `0`.

```ini
[WinntDirectories]
a="Bart",3                     ; always-created folder "Bart" in the root
b="Project\Bart\Source",3      ; nested folder in the root
c="extra\files",1              ; folder inside the i386 directory
```

### Predefined directory IDs

`pluginformat.htm` lists the most-used standard IDs (target paths are relative to
the PE system root unless noted):

| ID | Directory | Notes |
|----|-----------|-------|
| `30000` | *(output root)* | PE Builder output root. IDs ≥ 30000 are used internally/dynamically. |
| `1` | `\` (SystemRoot) | `\i386` on CD, `\minint` on hard disk. |
| `2` | `system32` | |
| `3` | `system32\config` | Registry hive location. |
| `4` | `system32\drivers` | |
| `5` | `system` | |
| `17` | `system32\drivers\etc` | |
| `20` | `inf` | |
| `21` | `Help` | |
| `22` | `Fonts` | |
| `24` | `msagent\intl` | |
| `52` | `msagent` | |
| `124` | `WinSxS` | |
| `125` | `WinSxS\Manifests` | |
| `252` | `WinSxS\Policies` | |

(The full running table also includes the spool/printer, WBEM, Java, cursors,
media, web, and setup directories, numbered roughly 9–37. Binary analysis shows
the real table is larger still — directory IDs well above 37 are referenced
internally, and a `@NN@` token expands to the path of directory ID `NN`. See
[`07-engine-internals.md`](07-engine-internals.md).)

---

## `[SourceDisksFiles]`

Copies (and optionally decompresses) individual files into the output. Supports
the `.BuildNo` suffix.

**Format:** `filename=directoryID[, filenameRenamed][, attribute]`

- `filename` — the file to copy. For a compressed source file, give the
  **decompressed** name. May include an absolute/relative path or a variable such
  as `@SourcePath@\i386\...`. **Wildcards are allowed** (e.g.
  `flash\*flash.ocx*=2,swflash.ocx,4`).
- `directoryID` — the target directory (see `[WinntDirectories]`).
- `filenameRenamed` — optional target filename if renaming.
- `attribute` — a combinable bitfield:

| Value | Meaning |
|-------|---------|
| `1` | Used for **pluginCheck()**: the file must exist in the same directory as the `.inf` before the plugin can be enabled/continue. (I.e. the file ships *with* the plugin.) |
| `2` | **Do not decompress** the file — copy the compressed archive (`?????.??_`) as-is. |
| `4` | Copy the file **only if it exists**; do not complain if missing (optional file). |
| `8` | Rename the filename to **uppercase** (required for booting from CD). |
| `0` | Default (decompress if compressed; copy required file). |

File-existence checks for attributes `1`/`4` are performed against the
*decompressed* name; for attribute `2` against the compressed archive itself.

```ini
[SourceDisksFiles]
xpeinit.exe=2,,1               ; copy to system32; ships with the plugin
oeminfo.ini=2,,4               ; optional
system\comdlg32.ocx=2,,4       ; optional, from a subfolder
ocgen.dll=32                   ; default attribute
```

## `[SourceDisksFolders]`

Copies whole folders. Supports the `.BuildNo` suffix.

**Format:** `sourcepath=directoryID`

- `sourcepath` — the source path/folder to copy from. This is an **exact copy** —
  nothing is decompressed or extracted.
- `directoryID` — the target directory.

---

## Registry sections

Registry directives share a common line grammar; the **section name selects the
target hive**.

| Section | Target hive | Subkey is relative to |
|---------|-------------|-----------------------|
| `[Software.AddReg]` / `[Software.DelReg]` | SOFTWARE | `HKLM\Software` |
| `[SetupReg.AddReg]` / `[SetupReg.DelReg]` | SYSTEM | the system hive root (e.g. `ControlSet001\...`) |
| `[Default.AddReg]` / `[Default.DelReg]` | Default user | the default user hive (`.DEFAULT` / `NTUSER`) |

All support the `.BuildNo` suffix.

### `*.AddReg` line grammar

```
type,"subkey","valuename",data
```

- `type` — the **registry value type**, given as the actual `REG_*` constant:

  | `type` | Registry type | `data` form |
  |--------|---------------|-------------|
  | `0x1` | `REG_SZ` | quoted string |
  | `0x2` | `REG_EXPAND_SZ` | quoted string (may contain `%SystemRoot%` etc.) |
  | `0x3` | `REG_BINARY` | comma-separated hex bytes (e.g. `98,20,00,80`) |
  | `0x4` | `REG_DWORD` | `0x`-prefixed dword |
  | `0x7` | `REG_MULTI_SZ` | string(s) |

- `subkey` — the key path relative to the hive root for the section.
- `valuename` — the value name; **empty** (`,,`) denotes the key's **default**
  value.
- `data` — the value data, per the type.

```ini
[Software.AddReg]
0x1,"Microsoft\Windows NT\CurrentVersion","RegisteredOwner","XPE"
0x2,"...\Policies\System","SetVisualStyle","%SystemRoot%\Resources\Themes\Luna.theme"
0x4,"Microsoft\Windows\CurrentVersion\Setup","LogLevel",0x0
0x3,"Control Panel\Desktop","UserPreferencesMask",98,20,00,80
```

### `*.DelReg` line grammar

```
"subkey"[,"valuename"]
```

Deletes a key (or a single value within it).

---

## Text / INI patching sections

These sections modify files that already exist in the output (typically Windows
setup `.inf` files, `txtsetup.sif`, or `nu2menu.xml`). All operate on a named
target file. Embedded quotes are **doubled**; note that percent signs in setup
INF strings appear doubled (`%%...%%`) because they pass through Windows INF
string substitution.

### `[SetValue]`

Sets (creates or updates) a value in an INI-style file.

**Format:** `"file", "section", "key"[, "value"]`

```ini
[SetValue]
"txtsetup.sif", "SetupData", "osloadoptions", """/fastdetect /minint"""
"txtsetup.sif", "SetupData", "loaderprompt",  """Starting BartPE..."""
"netmscli.inf", "Messenger_Service_Inst", "StartType", "2"
```

(The base manifest uses this to set the crucial `/minint` boot option — see
[`02-build-process.md`](02-build-process.md).)

### `[AddLine]`

Adds a raw line to a section of a target file.

**Format:** `"file", "section", "line"`

```ini
[AddLine]
"netmscli.inf", "MSClient.DelReg", "HKLM, Software\Microsoft\Rpc\SecurityService, 9"
"nettcpip.inf", "Registry.MS_TCPIP", "    HKLM,SOFTWARE\Microsoft\Rpc\ClientProtocols,ncacn_nb_tcp,,""rpcrt4.dll"""
```

### `[DelLine]`

Removes matching line(s) from a section of a target file.

**Format:** `"file", "section", "linePrefix"`

```ini
[DelLine]
"nettcpip.inf", "Registry.MS_TCPIP.PrimaryInstall", "HKR,Ndi,HelpText,"
"netmscli.inf", "MSClient.Browser.AddReg", "HKR, Parameters, MaintainServerList,"
```

Together, `[AddLine]` / `[DelLine]` let the base manifest rewrite Microsoft's own
network setup `.inf` files so that networking installs correctly under the
PE-mode registry.

### `[Append]`

Appends the contents of one file to another.

**Format:** `Filename, FileNameToAppend`

```ini
[Append]
nu2menu.xml, test.xml
```

In practice `[Append]` is used to extend the Nu2Menu configuration
(`nu2menu.xml`) with additional menu XML — this is how plugins add their own
entries to the default menu shell.

---

## Section quick-reference

| Section | Purpose |
|---------|---------|
| `[Version]` | Required signature so setupapi can parse the file. |
| `[PEBuilder]` | Required. Name / Enable / Help / Config. |
| `[WinntDirectories]` | Define output directories and their IDs. |
| `[SourceDisksFiles]` | Copy/decompress individual files. |
| `[SourceDisksFolders]` | Copy whole folders verbatim. |
| `[Software.AddReg]` / `.DelReg` | SOFTWARE hive registry edits. |
| `[SetupReg.AddReg]` / `.DelReg` | SYSTEM hive registry edits. |
| `[Default.AddReg]` / `.DelReg` | Default-user hive registry edits. |
| `[SetValue]` | Set an INI/SIF value in an output file. |
| `[AddLine]` / `[DelLine]` | Add/remove raw lines in an output file section. |
| `[Append]` | Append one file to another (Nu2Menu XML). |

All copy/registry/text sections accept the `.BuildNo` suffix for
source-conditional application.