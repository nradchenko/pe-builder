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
# perun.c is compiled into *both* outputs rather than the EXE linking the DLL. The list
# semantics then physically cannot drift between the two front ends, which is the point of
# having one implementation — and perun.exe stays runnable on its own, without perun.dll
# beside it, which matters because it is the tool you reach for when something is wrong.
ARCH ?= i386

ifeq ($(ARCH),amd64)
  TRIPLE := x86_64-w64-mingw32
else ifeq ($(ARCH),i386)
  TRIPLE := i686-w64-mingw32
else
  $(error unknown ARCH '$(ARCH)' — expected i386 or amd64)
endif

CC      := $(TRIPLE)-gcc
WINDRES := $(TRIPLE)-windres
# -Wl,--no-insert-timestamp zeroes the PE TimeDateStamp for reproducible output.
LDFLAGS := -Wl,--no-insert-timestamp
CFLAGS  := -O2 -s -Wall -Wextra

# Attribution for the VERSIONINFO resource. pebuild passes all of these to the plugin's
# [Build]; the defaults are what a by-hand `make` in this directory gets. VERSION_NUM is
# the version reduced to the four numbers FILEVERSION takes — derived once, in Go
# (buildinfo.VersionQuad), because as shell here it drifted and got it wrong.
PEBUILD_VERSION     ?= dev
PEBUILD_VERSION_NUM ?= 0,0,0,0
PEBUILD_PRODUCT     ?= pe-builder
PEBUILD_COMPANY     ?= Nikita Radchenko
PEBUILD_COPYRIGHT   ?= Copyright 2026 Nikita Radchenko. Apache-2.0
VER_STR             := $(PEBUILD_VERSION)
VER_NUM             := $(PEBUILD_VERSION_NUM)
# The resource template is shared, so the build materializes it here rather than the
# plugin shipping a copy; the fallback is the in-tree path, for a by-hand build.
VER_RC              := $(firstword $(wildcard pebuild/version.rc ../.shared/version.rc))

# The two outputs need separate resource objects: they differ in FILETYPE (an EXE is VFT_APP, a
# DLL is VFT_DLL) and in the name each reports as its own.
VERFLAGS = -DVER_NUM='$(VER_NUM)' -DVER_STR='\"$(VER_STR)\"' \
           -DVER_PRODUCT='\"$(PEBUILD_PRODUCT)\"' \
           -DVER_COMPANY='\"$(PEBUILD_COMPANY)\"' \
           -DVER_COPYRIGHT='\"$(PEBUILD_COPYRIGHT)\"' \
           -DVER_INTERNAL='\"perun\"'

.PHONY: all clean

all: perun.dll perun.exe

version-dll.o: $(VER_RC)
	$(WINDRES) $(VERFLAGS) -DVER_FILETYPE=VFT_DLL \
	           -DVER_DESC='\"Startup run-list walker (in-process)\"' \
	           -DVER_ORIGINAL='\"perun.dll\"' $< -o $@

version-exe.o: $(VER_RC)
	$(WINDRES) $(VERFLAGS) -DVER_FILETYPE=VFT_APP \
	           -DVER_DESC='\"Startup run-list walker\"' \
	           -DVER_ORIGINAL='\"perun.exe\"' $< -o $@

# PERUN_EXPORTS turns the perun.h declarations into dllexports; only the DLL wants them.
perun.dll: perun.c perundll.c perun.h version-dll.o
	$(CC) -shared -DPERUN_EXPORTS $(CFLAGS) $(LDFLAGS) -o $@ perun.c perundll.c version-dll.o \
	      -ladvapi32 -lkernel32

perun.exe: perun.c perunmain.c perun.h version-exe.o
	$(CC) -mconsole $(CFLAGS) $(LDFLAGS) -o $@ perun.c perunmain.c version-exe.o \
	      -ladvapi32 -lkernel32 -lshell32

clean:
	rm -f perun.dll perun.exe version-dll.o version-exe.o
