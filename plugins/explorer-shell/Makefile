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
#
# Why peshut links freestanding, and why its entry point is named per architecture:
# see README.md § Why the build is freestanding.
ARCH ?= i386

ifeq ($(ARCH),amd64)
  TRIPLE := x86_64-w64-mingw32
else ifeq ($(ARCH),i386)
  TRIPLE := i686-w64-mingw32
else
  $(error unknown ARCH '$(ARCH)' — expected i386 or amd64)
endif

# The toolchain programs (CC, WINDRES) and the VERSIONINFO attribution come from the shared
# settings, materialized here by the build; ../.shared is the by-hand fallback. It needs TRIPLE,
# so it is included after the switch above.
include $(firstword $(wildcard pebuild/common.mk ../.shared/common.mk))
ifndef PEBUILD_COMMON
$(error no common.mk at pebuild/common.mk or ../.shared/common.mk)
endif
# -Wl,--no-insert-timestamp zeroes the PE TimeDateStamp for reproducible output.
LDFLAGS := -Wl,--no-insert-timestamp

# The two outputs need separate resource objects: they differ in FILETYPE (an EXE is VFT_APP, a
# DLL is VFT_DLL) and in the name each reports as its own, so each gets its own generated header.

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

version-shell.o: $(VER_RC)
	@$(call verinfo,verinfo-shell.h,Explorer shell launcher,peshell,peshell.exe,VFT_APP)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo-shell.h\"' $< -o $@

version-shut.o: $(VER_RC)
	@$(call verinfo,verinfo-shut.h,Shell power-off hook,peshut,peshut.dll,VFT_DLL)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo-shut.h\"' $< -o $@

peshell.exe: peshell.c startmenu.c startmenu.h version-shell.o
	$(CC) -O2 -s -mwindows -Wall -Wextra $(LDFLAGS) -o $@ peshell.c startmenu.c version-shell.o -lole32 -luuid -lshell32 -ladvapi32 -lversion

# The resource object is data only — no code, no CRT reference — so it links into the
# freestanding DLL without disturbing what PESHUT_FLAGS establishes.
peshut.dll: peshut.c version-shut.o
	$(CC) -shared -O2 -s -Wall -Wextra $(LDFLAGS) $(PESHUT_FLAGS) -o $@ peshut.c version-shut.o $(PESHUT_LIBS)

clean:
	rm -f peshell.exe peshut.dll version-shell.o version-shut.o verinfo-shell.h verinfo-shut.h
