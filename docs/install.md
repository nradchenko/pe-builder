# Installing and building on each platform

pe-builder is a Go program plus a set of plugins, some of which compile from source at build
time. The **released binaries** (see the project's releases) are the easy way to get `pebuild`
and `pewalk`; you can also build them yourself.

What differs by platform is the **toolchain a plugin's payload needs**. A bare `cmd.exe` image
with no plugins is pure Go and needs nothing beyond `pebuild` itself. Plugins that ship source
rather than a binary compile small C programs and kernel drivers with the **MinGW-w64
cross-compiler**, so that has to be present and on `PATH`. That is the desktop set
(`explorer-shell`, `luna`, `ramdrive`, `pesuite`) and also `poweroff` and `startup` — the last
of which a console-only image may well enable on its own, so the toolchain is not a
desktop-only requirement.

Per-platform status at a glance:

| Platform | Status |
|---|---|
| **Linux** | primary — developed and tested here |
| **Windows** | tested — all image types build and boot; needs the environment setup below |
| **macOS** | experimental — the binaries compile, but nothing has been tested at runtime |

## Linux

The supported platform. Build both tools with `make`:

```sh
make                                # builds bin/pebuild and bin/pewalk
./bin/pebuild build examples/minimal.hcl
```

You need Go (see `go.mod` for the version). For a **desktop** image, also install the
MinGW-w64 cross-compiler and `make`:

```sh
# Debian / Ubuntu
sudo apt install gcc-mingw-w64-i686 make          # 32-bit images (a WinXP source)
sudo apt install gcc-mingw-w64-x86-64             # additionally, for 64-bit (XP x64) images
```

Other distributions ship the same toolchain under their own package names. A **`cmd.exe`**
image needs only Go.

## Windows

**Status: tested.** `pebuild` builds and boots all three image types on Windows — a bare
`cmd.exe` image, the classic Explorer desktop, and the themed Luna desktop — verified on
Windows 10/11. A bare `cmd.exe` image needs nothing but `pebuild.exe`. A **desktop** image
needs the MinGW-w64 toolchain, and there are two rough edges (below) until the Makefile
portability fix lands.

### A `cmd.exe` image (no toolchain)

Download `pebuild.exe` from the releases, and build a minimal manifest against your XP source:

```
pebuild.exe build minimal.hcl
```

### A desktop image (toolchain via MSYS2)

1. Install **[MSYS2](https://www.msys2.org)** (default location `C:\msys64`).
2. In the MSYS2 shell, update and install the toolchain:
   ```sh
   pacman -Syu           # run twice if it asks you to reopen the shell
   pacman -S mingw-w64-i686-gcc make
   ```
3. Set up the build environment. Two workarounds are needed for now — see *Known rough
   edges*:
   ```sh
   export PATH=/mingw32/bin:$PATH
   ln -s /mingw32 /usr/i686-w64-mingw32     # see the caveat below
   ```
4. Build from that same MSYS2 shell:
   ```sh
   pebuild.exe build luna.hcl
   ```

### Known rough edges (Windows)

- **The `ln -s` above deep-copies, it does not link.** MSYS2 emulates symlinks by copying the
  tree unless `MSYS=winsymlinks:nativestrict` is set, so the command duplicates `/mingw32`
  (a few hundred MB) into `/usr/i686-w64-mingw32` — which is why it takes a few seconds. It
  works, but it is a snapshot: if you later `pacman -Syu` the toolchain, re-run it to refresh
  the copy.
- **Why the copy is needed at all:** the plugin Makefiles hardcode the Debian cross-toolchain
  layout (`-I/usr/$(TRIPLE)/include/ddk`), which does not match where MSYS2 keeps its DDK
  headers (`/mingw32/include/ddk`). The copy makes the Debian path resolve. A Makefile change
  to make that path overridable is planned, after which the symlink step goes away.

## macOS

**Status: experimental — not yet tested.** The binaries are pure Go and cross-compile to
macOS (both Intel and Apple Silicon), and a `cmd.exe` image should build with only Go, but
**nothing has been run on macOS**, so treat it as unverified and please report what happens.

A desktop image would need the MinGW-w64 cross-compiler, available via Homebrew:

```sh
brew install mingw-w64
```

The same Makefile assumption that bites on Windows (the hardcoded Debian DDK path) very likely
applies here too, since Homebrew keeps its headers elsewhere again — expect to need the same
kind of workaround until the Makefile fix lands. Reports welcome.
