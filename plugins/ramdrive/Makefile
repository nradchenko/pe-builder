# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile ramdrive.sys (a native NT kernel driver) for the /minint PE, using
# only a MinGW-w64 toolchain — no WDK, no MSVC:
#   i386:   apt install gcc-mingw-w64-i686
#   amd64:  apt install gcc-mingw-w64-x86-64
#
# The driver is a freestanding PE with the NT-native subsystem, entry point
# DriverEntry, and imports bound against ntoskrnl.exe/hal.dll (mingw ships the
# import libs and the ddk/ headers). ASLR/NX bits are cleared (an XP-era driver).
# Kernel drivers need a valid PE checksum (the XP image loader validates it);
# current GNU ld writes one at link time, so the recipe only guards against an
# older binutils that would leave it zero (see the check below).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386. The build
# passes it as an explicit make argument (see the plugin's [Build.<arch>] Command), so
# the whole per-arch difference lives here in one switch. See README.md § Build.
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
TARGET  := ramdrive.sys
SRC     := ramdrive.c

CFLAGS  := -ffreestanding $(ARCHDEF) -O2 -Wall -Wextra -Wno-multichar \
           -fno-asynchronous-unwind-tables \
           -I/usr/$(TRIPLE)/include/ddk

LDFLAGS := -nostdlib -shared \
           -Wl,--subsystem,$(SUBSYS) \
           -Wl,--image-base,0x10000 \
           -Wl,--entry,$(ENTRY) \
           -Wl,--file-alignment,0x200 \
           -Wl,--section-alignment,0x1000 \
           -Wl,--disable-dynamicbase -Wl,--disable-nxcompat \
           -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols
LDLIBS  := -lntoskrnl -lhal

.PHONY: all clean

all: $(TARGET)

$(TARGET): $(SRC)
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ $(SRC) $(LDLIBS)
	@$(OBJDUMP) -p $@ | awk '/CheckSum/ && $$2 ~ /^0+$$/ \
		{ print "ERROR: $@ has a zero PE checksum — the driver will not load; " \
		        "update binutils (link-time checksum) or add a checksum step"; exit 1 }'

clean:
	rm -f $(TARGET)