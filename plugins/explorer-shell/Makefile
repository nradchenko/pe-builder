# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the Explorer-shell binaries for a Windows PE:
#   peshell.exe — the Setup\CmdLine launcher
#   peshut.dll  — the ExitWindowsEx power-off hook (loaded via AppInit_DLLs)
# Requires a MinGW-w64 toolchain:
#   i386:   apt install gcc-mingw-w64-i686
#   amd64:  apt install gcc-mingw-w64-x86-64
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. These are user-mode binaries, so the architecture
# changes nothing but the toolchain — no arch define, no entry-point decoration, no
# subsystem version, unlike the driver plugins.
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

# peshut.dll is injected before the host process's CRT is initialized, so on amd64 it must
# not link one in: MinGW's startup stubs would pull in msvcrt and the process would abort
# with R6030. Freestanding, it imports only kernel32/advapi32. x86 stays on the stock link
# line — it is the build that was verified by booting. See README.md § Shutdown.
PESHUT_FLAGS :=
PESHUT_LIBS  := -ladvapi32
ifeq ($(ARCH),amd64)
  PESHUT_FLAGS := -nostdlib -nodefaultlibs -Wl,--entry,DllMain
  PESHUT_LIBS  := -ladvapi32 -lkernel32
endif

.PHONY: all clean

all: peshell.exe peshut.dll

peshell.exe: peshell.c startmenu.c startmenu.h
	$(CC) -O2 -s -mwindows -Wall -Wextra $(LDFLAGS) -o $@ peshell.c startmenu.c -lole32 -luuid -lshell32 -ladvapi32 -lversion

peshut.dll: peshut.c
	$(CC) -shared -O2 -s -Wall -Wextra $(LDFLAGS) $(PESHUT_FLAGS) -o $@ $< $(PESHUT_LIBS)

clean:
	rm -f peshell.exe peshut.dll
