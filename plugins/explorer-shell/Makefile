# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the Explorer-shell binaries for a Windows PE:
#   peshell.exe — the Setup\CmdLine launcher
#   peshut.dll  — the ExitWindowsEx power-off hook (loaded via AppInit_DLLs)
# Needs a MinGW-w64 toolchain (see docs/install.md for the per-platform setup).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. These are user-mode binaries, so the architecture picks
# the toolchain and nothing else — no arch define and no subsystem version, unlike the driver
# plugins. The one exception is peshut.dll's entry-point symbol, which i386 decorates; see
# the note on PESHUT_FLAGS below.
#
# Both must match the PE's architecture. peshut.dll especially: it is injected into other
# processes through AppInit_DLLs, and a process only loads a DLL of its own bitness — a
# 32-bit hook cannot enter a native amd64 explorer, and there is no WOW64 path for it.
ARCH ?= i386

ifeq ($(ARCH),amd64)
  TRIPLE := x86_64-w64-mingw32
else ifeq ($(ARCH),i386)
  TRIPLE := i686-w64-mingw32
else
  $(error unknown ARCH '$(ARCH)' — expected i386 or amd64)
endif

CC      := $(TRIPLE)-gcc
# -Wl,--no-insert-timestamp zeroes the PE TimeDateStamp for reproducible output.
LDFLAGS := -Wl,--no-insert-timestamp

# peshut.dll must link no CRT, on either architecture — see README.md § Why the build is
# freestanding. That means naming the entry point, and only i386 decorates it (stdcall). A
# name the linker cannot find is merely a *warning*: it leaves the start address at 0 and
# emits a DLL that links cleanly but cannot load, so check `objdump -f` after touching this.
PESHUT_FLAGS := -nostdlib -nodefaultlibs
PESHUT_LIBS  := -ladvapi32 -lkernel32
ifeq ($(ARCH),amd64)
  PESHUT_FLAGS += -Wl,--entry,DllMain
else
  PESHUT_FLAGS += -Wl,--entry,_DllMain@12
endif

.PHONY: all clean

all: peshell.exe peshut.dll

peshell.exe: peshell.c startmenu.c startmenu.h
	$(CC) -O2 -s -mwindows -Wall -Wextra $(LDFLAGS) -o $@ peshell.c startmenu.c -lole32 -luuid -lshell32 -ladvapi32 -lversion

peshut.dll: peshut.c
	$(CC) -shared -O2 -s -Wall -Wextra $(LDFLAGS) $(PESHUT_FLAGS) -o $@ $< $(PESHUT_LIBS)

clean:
	rm -f peshell.exe peshut.dll
