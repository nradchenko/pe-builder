# Inspecting a source or a build

The readers the build runs on are also available as commands, so you can answer
questions about a Windows installation source — or about a PE you just built —
without unpacking anything by hand. Every command takes a source as a directory
or an `.iso`, and reads it the same way a build does.

## Identifying a source

```sh
pebuild source probe win.iso
```

Reports the media's build number, architecture, and flavor.

## Extracting a file

```sh
# decompressed, whatever form the media keeps it in
pebuild source extract win.iso ntoskrnl.exe mouhid.sys -o ./files
```

A Windows source stores a file in one of three forms, and a bare filename
resolves against all of them: a loose file (`I386\NTOSKRNL.EXE`), a compressed
single-file archive (`I386\NTOSKRNL.EX_`), or a member packed inside
`DRIVER.CAB` / `SP3.CAB`. Files are written under their canonical decompressed
name — extracting `NTOSKRNL.EX_` gives you `ntoskrnl.exe`, with the real name
taken from the archive's own directory rather than guessed from the truncated
extension.

Two variations:

- **An exact path** (`pebuild source extract win.iso i386/lang/foo.dll`) addresses
  a file whose basename is not unique on the media, which a name lookup cannot
  disambiguate.
- **`-o -`** writes a single file to stdout, to pipe into another tool.
- **`--raw`** writes the stored bytes verbatim, without decompressing, under the name
  the media stores them under: a loose file comes out as itself, and `ntoskrnl.exe`
  comes out as the cabinet `ntoskrnl.ex_` rather than being mislabelled as the PE. A
  file packed inside `DRIVER.CAB` has no stored form of its own — its bytes exist only
  as part of the cabinet's compressed stream — so it cannot be read raw, and the error
  says which cabinet holds it.

## Listing files and where they land

```sh
pebuild source ls win.iso 'kbd*'
```

```
NAME          STORED AS                       TARGET
kbd101.dll    I386/KBD101.DL_ (compressed)    i386/system32/kbd101.dll
kbd101b.dll   I386/DRIVER.CAB (cabinet)       i386/system32/kbd101b.dll
kbd106.dll    I386/SP3.CAB (cabinet)          i386/system32/kbd106.dll
kbda1.dll     I386/KBDA1.DLL                  i386/system32/kbda1.dll
```

For each file the source's metadata declares: the form the media stores it in,
and the output-tree path a build would place it at. It answers "is this file even
on the media, and where does it end up" in one look.

A file the metadata declares but the media does not carry is shown as `not on
media`. `--all` additionally lists files that are present on the media but that
the metadata does not declare; those have no placement, shown as `-`, since
nothing would place them.

## The directory-ID table

```sh
pebuild source dirs win.iso
```

Prints `[WinntDirectories]` from the source's `txtsetup.sif` — the directory IDs
that file placements refer to, the path each maps to relative to the system root,
and the output-tree path a build writes that directory under.

## INF sections

```sh
pebuild source inf win.iso txtsetup.sif nls
```

```
[nls]
AnsiCodepage = c_1252.nls, 1252
OemCodepage = c_437.nls, 437, c_850.nls, 850
MacCodepage = c_10000.nls, 10000
UnicodeCasetable = l_intl.nls, 0409
OemHalFont = vgaoem.fon
DefaultLayout = 00000409
```

Parses an INF off the source — `txtsetup.sif`, `layout.inf`, `hivesys.inf`, a
driver INF — with the same parser the build uses, and prints one section. Values
appear as the engine sees them: `%TOKEN%` substituted from `[Strings]`,
continuation lines joined, quotes stripped. `--raw` prints lines as written
instead.

Without a section name, the sections the file declares are listed. Sections
conditioned on a build number resolve against the source's own build unless
`--build` overrides it.

## Registry hives

```sh
# which shell does the PE you built launch?
pebuild hive dump out/pe.iso 'HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\Shell'

# a whole service key, or one level of a large one
pebuild hive dump out/pe.iso 'SYSTEM\ControlSet001\Services\atapi'
pebuild hive dump win.iso 'SYSTEM\ControlSet001\Services' --depth 1
```

The target is a built PE (an output directory, a work directory, or a mastered
`.iso`), a Windows installation source, or a hive file on its own. The first
component of the registry path names the hive — `SYSTEM`, `SOFTWARE`, `DEFAULT`,
`SAM`, `SECURITY`, or `SETUPREG` — and pebuild locates that hive within the
target; a leading `HKLM\` is accepted and ignored. `--file` names a hive inside
the target directly instead.

Naming a value rather than a key prints just that value. Values render by type,
in the shape the registry's own export format uses:

```
[System\ControlSet001\Services\ramdrive]
    DisplayName = "PE RAM Drive"
    ErrorControl = dword:00000001 (1)
    ImagePath = "system32\drivers\ramdrive.sys"
    Start = dword:00000003 (3)
```

Backslashes in string values are shown as they are, so a path can be read and
pasted back. Binary values are truncated to their first 32 bytes unless `--full`
is given.

`--depth` bounds how many levels of subkeys are descended, which keeps a large
key (`Services`, `Classes`) to a summary rather than a flood.

### Where SYSTEM lives

The SYSTEM hive is not where its name suggests. A build writes it to
`system32\setupreg.hiv`, because in `/minint` mode the loader loads
`setupreg.hiv` as the SYSTEM hive and never consults `config\SYSTEM`; a Windows
installation source carries its own skeletal `setupreg.hiv` at the root of the
system root. Both are found by the name `SYSTEM` (and by `SETUPREG`, which is the
name the file actually has on the media).
