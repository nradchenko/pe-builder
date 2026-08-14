# Installing and building on each platform

pe-builder is a Go program plus a set of plugins, some of which compile from source at build
time. The **released binaries** (see the project's releases) are the easy way to get `pebuild`
and `pewalk`; you can also build them yourself.

What differs by platform is the **toolchain a plugin's payload needs**. A bare `cmd.exe` image
with no plugins is pure Go and needs nothing beyond `pebuild` itself. Plugins that ship source
rather than a binary compile small C programs and kernel drivers with the **MinGW-w64
cross-compiler**, so that has to be present and on `PATH`. That is the desktop set
(`explorer-shell`, `luna`, `ramdrive`, `pesuite`) and also `poweroff`, `startup` and
`hardware` — the last two of which a console-only image may well enable on its own
(`network-tools` pulls `hardware` in), so the toolchain is not a
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
needs the MinGW-w64 toolchain.

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
3. Put the toolchain on `PATH`:
   ```sh
   export PATH=/mingw32/bin:$PATH
   ```
4. Build from that same MSYS2 shell:
   ```sh
   pebuild.exe build luna.hcl
   ```

The kernel-driver plugins need the MinGW DDK headers, which MSYS2 keeps at
`/mingw32/include/ddk`. Their Makefiles locate that directory from the compiler itself, so
nothing has to be configured; if your toolchain keeps them somewhere neither the MSYS2 nor
the Debian layout predicts, `export DDK_INCLUDE=<directory holding ntddk.h>` points them at it.

## macOS

**Status: experimental — not yet tested.** The binaries are pure Go and cross-compile to
macOS (both Intel and Apple Silicon), and a `cmd.exe` image should build with only Go, but
**nothing has been run on macOS**, so treat it as unverified and please report what happens.

A desktop image would need the MinGW-w64 cross-compiler, available via Homebrew:

```sh
brew install mingw-w64
```

The driver plugins locate the DDK headers from the compiler rather than from a fixed path, so
Homebrew's layout should need no configuration; if it is not found, `export
DDK_INCLUDE=<directory holding ntddk.h>` points the build at it. Reports welcome.

## Shell completion

`pebuild` and `pewalk` both generate completion scripts for **bash**, **zsh**, **fish**, and
**PowerShell**. To enable completion for your current shell session (bash shown):

```sh
source <(pebuild completion bash)
source <(pewalk completion bash)
```

Each tool's `completion` sub-command prints its own setup instructions for the chosen shell —
the per-session form, the permanent-install path (which varies by shell and OS), and any
prerequisite (bash needs the `bash-completion` package installed):

```sh
pebuild completion bash --help     # or: zsh, fish, powershell
```
