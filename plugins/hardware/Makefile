# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the runtime hardware configurator for a Windows PE:
#   pehw.exe — the device-install pass, run from the startup task list
# Needs a MinGW-w64 toolchain (see docs/install.md for the per-platform setup).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. A plain user-mode binary, so the architecture picks the
# toolchain and nothing else.
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

PEBUILD_VERSION   ?= dev
PEBUILD_PRODUCT   ?= pe-builder
PEBUILD_COMPANY   ?= Nikita Radchenko
PEBUILD_COPYRIGHT ?= Copyright 2026 Nikita Radchenko. Apache-2.0
VER_STR           := $(PEBUILD_VERSION)
# FILEVERSION takes four comma-separated numbers, so a non-numeric version ("dev", "0.5.0-rc1")
# has to become one. Only the leading numeric components are kept, padded out with zeroes.
# The trailing newline matters: without it a version with no leading digits ("dev", the default
# for an unstamped build) leaves awk with no record at all, and windres is handed an empty
# FILEVERSION and refuses the file.
VER_NUM := $(shell printf '%s\n' "$(VER_STR)" | sed 's/[^0-9.].*//' | awk -F. \
             '{printf "%d,%d,%d,0", $$1+0, $$2+0, $$3+0}')

.PHONY: all clean

all: pehw.exe

version.o: version.rc
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
