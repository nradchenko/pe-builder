# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the runtime hardware configurator for a Windows PE:
#   pehw.exe — the device-install pass, run from the startup task list
# Needs a MinGW-w64 toolchain (see docs/install.md for the per-platform setup).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. A plain user-mode binary, so the architecture picks the
# toolchain and nothing else. See README.md § Build.
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

.PHONY: all clean

all: pehw.exe

version.o: $(VER_RC)
	$(WINDRES) -DVER_NUM='$(VER_NUM)' -DVER_STR='\"$(VER_STR)\"' \
	           -DVER_PRODUCT='\"$(PEBUILD_PRODUCT)\"' \
	           -DVER_COMPANY='\"$(PEBUILD_COMPANY)\"' \
	           -DVER_COPYRIGHT='\"$(PEBUILD_COPYRIGHT)\"' \
	           -DVER_DESC='\"Runtime hardware and network configuration\"' \
	           -DVER_INTERNAL='\"pehw\"' -DVER_ORIGINAL='\"pehw.exe\"' \
	           -DVER_FILETYPE=VFT_APP $< -o $@

# ole32 is for the INetCfg component install (`pehw net`); netcfgx.dll itself is loaded and
# registered at run time rather than linked, so it is not named here.
pehw.exe: pehw.c version.o
	$(CC) -mconsole $(CFLAGS) $(LDFLAGS) -o $@ pehw.c version.o \
	      -lsetupapi -ladvapi32 -lole32 -lkernel32

clean:
	rm -f pehw.exe version.o
