# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the startup run-list binaries for a Windows PE:
#   perun.dll — the walk, loaded in-process by peshell with a progress callback
#   perun.exe — the same walk behind a command line, for re-running tasks by hand
# Needs a MinGW-w64 toolchain (see docs/install.md for the per-platform setup).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. These are user-mode binaries, so the architecture picks
# the toolchain and nothing else.
#
# perun.c is compiled into *both* outputs rather than the EXE linking the DLL, so the list
# semantics cannot drift between the two front ends — see README.md § Build.
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
CFLAGS  := -O2 -s -Wall -Wextra

# The two outputs need separate resource objects: they differ in FILETYPE (an EXE is VFT_APP, a
# DLL is VFT_DLL) and in the name each reports as its own, so each gets its own generated header.

.PHONY: all clean

all: perun.dll perun.exe

version-dll.o: $(VER_RC)
	@$(call verinfo,verinfo-dll.h,Startup run-list walker (in-process),perun,perun.dll,VFT_DLL)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo-dll.h\"' $< -o $@

version-exe.o: $(VER_RC)
	@$(call verinfo,verinfo-exe.h,Startup run-list walker,perun,perun.exe,VFT_APP)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo-exe.h\"' $< -o $@

# PERUN_EXPORTS turns the perun.h declarations into dllexports; only the DLL wants them.
perun.dll: perun.c perundll.c perun.h version-dll.o
	$(CC) -shared -DPERUN_EXPORTS $(CFLAGS) $(LDFLAGS) -o $@ perun.c perundll.c version-dll.o \
	      -ladvapi32 -lkernel32

perun.exe: perun.c perunmain.c perun.h version-exe.o
	$(CC) -mconsole $(CFLAGS) $(LDFLAGS) -o $@ perun.c perunmain.c version-exe.o \
	      -ladvapi32 -lkernel32 -lshell32

clean:
	rm -f perun.dll perun.exe version-dll.o version-exe.o verinfo-dll.h verinfo-exe.h
