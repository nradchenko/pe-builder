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

## The manifests a binary carries

```sh
pebuild source manifest win.iso explorer.exe
pebuild image manifest out/pe.iso explorer.exe
```

```
explorer.exe (from AMD64/EXPLORER.EX_ (compressed))

  id 1 (CREATEPROCESS_MANIFEST_RESOURCE_ID — the one the loader applies to a process), language 1033, 1 byte
  not well-formed XML: EOF
  contents: " "

  id 123, language 1033, 730 bytes
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
...
```

Prints every side-by-side manifest a Windows binary carries, and **the resource id
each one sits at**. The id is what makes the answer usable: the loader takes a
process's manifest from the resource numbered **1** and, having found one, ignores a
`.manifest` file placed beside the binary. So a binary whose real manifest is at
another id behaves differently from one that has none, and the two cannot be told
apart without looking.

The listing above is that case — a one-byte stub at id 1 that carries nothing yet is
enough to win, with the real manifest at 123. A resource that is not well-formed XML
is called out and its bytes quoted rather than written through, since whatever is
there may not be text.

`source manifest` names a binary the way `source extract` does, decompressing it
first; `image manifest` names one the way `image extract` does.

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

## What an image was built from

`verify` answers one question: what was this image built from, and is that still
what this manifest and this pebuild describe? It compares a build's *inputs* —
which is what an image on disk can be held against — and says which of them have
moved since.

```sh
# does this ISO match the manifest and the pebuild I have now?
pebuild verify out/pe.iso examples/luna.hcl

# no manifest handy — just show what the image records
pebuild verify out/pe.iso
```

Every build writes a receipt into the tree it assembles, which the media stage
carries into the image as `PEBUILD.TXT` at the root — so it travels with the ISO
through copying and renaming, and the booted environment can `type
X:\PEBUILD.TXT` to show its own provenance. It records the manifest, the Windows
source, the fully resolved plugin set with the option values the build settled
on, and a content hash of pebuild's own embedded base layer and built-in
plugins.

`verify` reads it back — from a mastered `.iso` or a built tree, the same two
forms `hive dump` accepts — recomputes each field from the current binary,
manifest and plugin directories, and reports per field:

```
pebuild version  v0.7.0
engine content   sha256:fc1c3d43…  OK
manifest         sha256:72b2f2a2…  OK
source           winxp build 2600 (i386)  OK
plugins          9 resolved  OK
options          1 differ  CHANGED
  pebuild version: image built with v0.6.0, this is v0.7.0
  options: explorer-shell.bpp: built with "32", now "16"

MISMATCH: this image was built from different inputs; see CHANGED above.
```

A field that matches shows its current value (`options  explorer-shell.bpp=32 (+7 more)`);
one that differs shows how many differ, and each difference is named underneath.
The closing verdict names the inputs it compared, so what it did **not** compare
is visible from the line itself.

It exits non-zero on a mismatch, so a script can gate on it. Three outcomes are
kept apart deliberately: a **mismatch**, an input that **could not be checked**
(the source has moved away, say — reported `UNKNOWN`, never as a difference), and
an image carrying **no receipt at all**, which means it was built before receipts
existed or not by pebuild — not that it fails to match.

The hash is of what pebuild *embeds*, not of the executable: the binary carries a
link-time timestamp, so its bytes change on every rebuild even with no source
change, and hashing it would report a false mismatch after every no-op rebuild.
A plugin from a drop-in directory is not embedded, so it carries its own content
hash instead.

The version line is reported without a verdict. A version string flags a dirty
tree but does not characterize it — two different sets of uncommitted edits give
the same string — so a differing version alone does not fail the check. It is
still called out beneath the table (`image built with v0.6.0, this is v0.7.0`),
because on a release build it is the readiest sign that a different engine
produced the image.

### The scope of a match

A match is over the inputs the receipt records. The engine hash covers pebuild's
embedded *content* — the base layer and the built-in plugins, which is what a
build's output most directly follows from — and not the Go code of the engine
itself, so a change confined to `hive/`, `internal/tree/` or `layout/` does not move it.
On a release build that shows up from the other side, in the version and commit
the binary is stamped with; the version note above is where you see it.

So `verify` answers "same inputs", not "same compiler output". If you have
changed engine code and need certainty about the image, rebuild it.

### Build taint

A build that used anything outside the vetted, in-tree set — pebuild's embedded
built-ins and its base layer — is recorded as **tainted**, with graded reasons:
an out-of-tree plugin (`out-of-tree:<name>`), one whose `[Build]` section ran
(`ran-build-code:<name>`), one that staged files it carried
(`shipped-binaries:<name>`), or a manifest that placed host files into the image
with `inject{}` (`manifest-inject`). The build logs one `build tainted:` line and
the receipt carries the reasons, so `verify` reports them too.

It labels; it does not forbid. No build is refused for being tainted —
`build.allow_plugin_builds` remains the gate that decides what may actually run.
A built-in build-capable plugin such as `poweroff` runs its own build and still
counts as clean: taint is about provenance, not about whether code ran.

## Files in a built image

The `source` commands read a Windows installation *source*, addressed through its
own declarations — `source ls` resolves a name against `txtsetup.sif`/`layout.inf`
and reports the storage form and target directory, and `source extract`
decompresses whichever form the media stores a file in. A **built** image has none
of that: the build already resolved, decompressed and placed everything. So those
commands fail on one before they can read a byte, and the `image` commands read it
as a plain filesystem instead:

```sh
# what is in this image?
pebuild image ls out/pe.iso
pebuild image ls out/pe.iso '*.cmd'
pebuild image ls out/pe.iso 'i386/system32/drivers/*' --long

# read a file out of it
pebuild image extract out/pe.iso i386/system32/startnet.cmd -o -
pebuild image extract out/pe.iso ntdetect.com -o /tmp
```

The image is a mastered `.iso`, an output directory, or a work directory — ours or
another tool's, which is what makes these the way to inspect a reference image.

A pattern matches against both the base name and the full path, so `startnet.cmd`,
`*.cmd` and `i386/system32/*.cmd` all work. Matching is **case-insensitive** by
design: the tree writes mixed case and the ISO stage uppercases, so requiring the
caller to know which they have would make the same command work on a work directory
and fail on the `.iso` built from it.

`extract` takes an exact path, or a bare name when it is unique in the image; an
ambiguous name is an error listing the candidates rather than a silent pick.
An invalid pattern is likewise an error — `foo[` reports a syntax error rather
than "0 file(s) matched", which would read as an answer about the image.

Files are written under their **lowercased base name**, so the same file extracted
from a work directory and from the `.iso` mastered from it lands under one name.
Two files that would write to the same name are refused, and every name is resolved
before anything is written — a run either extracts all of them or leaves the output
directory untouched:

```sh
$ pebuild image extract out/pe.iso I386/SYSTEM32/VGAOEM.FON I386/FONTS/VGAOEM.FON -o d
pebuild: "I386/SYSTEM32/VGAOEM.FON" and "I386/FONTS/VGAOEM.FON" both write
         "vgaoem.fon"; extract them separately
```

Nothing is decompressed — a built image holds files as the build placed them. (An
image built by another tool may still hold a compressed file, `SETUPREG.HI_` for
instance; that comes out as the stored bytes.)

### WIM-layout images

An image built with the `wim` layout is arranged differently: its ISO holds the boot
chain (`grldr`, `coreimg.gz`, `menu.lst`) and the payload **WIM**, `PE.wim`, and the
entire Windows file tree *and every registry hive* live inside that WIM rather than on
the ISO filesystem. `image` and `hive` read *through* `PE.wim` transparently, so the same
commands work on a wim image as on a classic one — `image ls` lists the payload tree
merged with the media-root files, `image extract` pulls a file out of the WIM, and `hive
dump` reads a hive sealed inside it:

```sh
pebuild image ls out/pe.iso i386/system32/ntoskrnl.exe
pebuild hive dump out/pe.iso 'SYSTEM\ControlSet001\Services\fltmgr'
pebuild image extract out/pe.iso PE.wim -o .   # the WIM itself, as a blob
```

The payload is read on demand — listing costs the WIM's metadata, and extracting one
file decodes only that file. If `PE.wim` itself cannot be read (a corrupt or unsupported
WIM), the commands fall back to the media root and point you at extracting `PE.wim` to
inspect with a dedicated WIM tool. `verify` is unaffected either way — it reads the build
receipt, which the `wim` tail also places on the media root next to `PE.wim`.
