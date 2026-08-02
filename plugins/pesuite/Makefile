# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Builds the pesuite driver with a MinGW-w64 toolchain — no WDK, no MSVC
# (see docs/install.md for the per-platform setup).
#
#   pesuite.sys — a system-start kernel driver that sets VER_SUITE_TERMINAL in the shared
#                 SuiteMask, the bit the WinStation session subsystem is gated on.
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386. The build
# passes it as an explicit make argument (see the plugin's [Build.<arch>] Command), so the
# whole per-arch difference — toolchain, arch define, entry-point decoration, subsystem
# version, ddk path — lives here in one switch. See README.md § Build.
ARCH ?= i386

ifeq ($(ARCH),amd64)
  TRIPLE   := x86_64-w64-mingw32
  ARCHDEF  := -D_AMD64_
  # x64 has one calling convention and no stdcall name decoration, and its native NT
  # subsystem version is 5.02 (Server 2003 / XP x64).
  ENTRY    := DriverEntry
  SUBSYS   := native:5.02
else ifeq ($(ARCH),i386)
  TRIPLE   := i686-w64-mingw32
  ARCHDEF  := -D_X86_
  ENTRY    := _DriverEntry@8
  SUBSYS   := native:5.01
else
  # Refuse an architecture this recipe has no flags for, rather than falling through to
  # the other one: an unrecognized value (a typo, or the wrong case) would otherwise
  # build a driver for the wrong architecture and say nothing, and a driver of the wrong
  # bitness does not load — a silent failure at boot rather than an error here.
  $(error unknown ARCH '$(ARCH)' — expected i386 or amd64)
endif

CC      := $(TRIPLE)-gcc
OBJDUMP := $(TRIPLE)-objdump
WINDRES := $(TRIPLE)-windres

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

# The driver is a freestanding PE with the NT-native subsystem, entry point DriverEntry,
# and imports bound against ntoskrnl.exe (mingw ships the import libs and the ddk/
# headers). ASLR/NX bits are cleared (an XP-era driver). Kernel drivers need a valid PE
# checksum (the XP image loader validates it); current GNU ld writes one at link time, so
# the recipe only guards against an older binutils that would leave it zero.
DRV_CFLAGS  := -ffreestanding $(ARCHDEF) -O2 -Wall -Wextra -Wno-multichar \
               -fno-asynchronous-unwind-tables \
               -I/usr/$(TRIPLE)/include/ddk

DRV_LDFLAGS := -nostdlib -shared \
               -Wl,--subsystem,$(SUBSYS) \
               -Wl,--image-base,0x10000 \
               -Wl,--entry,$(ENTRY) \
               -Wl,--file-alignment,0x200 \
               -Wl,--section-alignment,0x1000 \
               -Wl,--disable-dynamicbase -Wl,--disable-nxcompat \
               -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols
DRV_LDLIBS  := -lntoskrnl

.PHONY: all clean

all: pesuite.sys

# A driver is VFT_DRV rather than VFT_APP, and names the kind of driver it is in
# FILESUBTYPE — VFT2_DRV_SYSTEM, the catch-all for one that is not a printer, keyboard
# or other device class Windows enumerates by name.
version.o: $(VER_RC)
	$(WINDRES) -DVER_NUM='$(VER_NUM)' -DVER_STR='\"$(VER_STR)\"' \
	           -DVER_PRODUCT='\"$(PEBUILD_PRODUCT)\"' \
	           -DVER_COMPANY='\"$(PEBUILD_COMPANY)\"' \
	           -DVER_COPYRIGHT='\"$(PEBUILD_COPYRIGHT)\"' \
	           -DVER_DESC='\"Product suite mask driver\"' \
	           -DVER_INTERNAL='\"pesuite\"' -DVER_ORIGINAL='\"pesuite.sys\"' \
	           -DVER_FILETYPE=VFT_DRV -DVER_FILESUBTYPE=VFT2_DRV_SYSTEM $< -o $@

pesuite.sys: pesuite.c version.o
	$(CC) $(DRV_CFLAGS) $(DRV_LDFLAGS) -o $@ pesuite.c version.o $(DRV_LDLIBS)
	@$(OBJDUMP) -p $@ | awk '/CheckSum/ && $$2 ~ /^0+$$/ \
		{ print "ERROR: $@ has a zero PE checksum — the driver will not load; " \
		        "update binutils (link-time checksum) or add a checksum step"; exit 1 }'

clean:
	rm -f pesuite.sys version.o
