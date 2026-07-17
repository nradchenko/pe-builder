# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Builds the pesuite driver with the MinGW-w64 i686 toolchain — no WDK, no MSVC:
#   apt install gcc-mingw-w64-i686
#
#   pesuite.sys — a system-start kernel driver that sets VER_SUITE_TERMINAL in the shared
#                 SuiteMask, the bit the WinStation session subsystem is gated on.

CC      := i686-w64-mingw32-gcc
OBJDUMP := i686-w64-mingw32-objdump

# The driver is a freestanding PE with the NT-native subsystem, entry point DriverEntry,
# and imports bound against ntoskrnl.exe (mingw ships the import libs and the ddk/
# headers). ASLR/NX bits are cleared (an XP-era driver). Kernel drivers need a valid PE
# checksum (the XP image loader validates it); current GNU ld writes one at link time, so
# the recipe only guards against an older binutils that would leave it zero.
DRV_CFLAGS  := -ffreestanding -D_X86_ -O2 -Wall -Wextra -Wno-multichar \
               -fno-asynchronous-unwind-tables \
               -I/usr/i686-w64-mingw32/include/ddk

DRV_LDFLAGS := -nostdlib -shared \
               -Wl,--subsystem,native:5.01 \
               -Wl,--image-base,0x10000 \
               -Wl,--entry,_DriverEntry@8 \
               -Wl,--file-alignment,0x200 \
               -Wl,--section-alignment,0x1000 \
               -Wl,--disable-dynamicbase -Wl,--disable-nxcompat \
               -Wl,--no-insert-timestamp -Wl,--exclude-all-symbols
DRV_LDLIBS  := -lntoskrnl

.PHONY: all clean

all: pesuite.sys

pesuite.sys: pesuite.c
	$(CC) $(DRV_CFLAGS) $(DRV_LDFLAGS) -o $@ $< $(DRV_LDLIBS)
	@$(OBJDUMP) -p $@ | awk '/CheckSum/ && $$2 ~ /^0+$$/ \
		{ print "ERROR: $@ has a zero PE checksum — the driver will not load; " \
		        "update binutils (link-time checksum) or add a checksum step"; exit 1 }'

clean:
	rm -f pesuite.sys
