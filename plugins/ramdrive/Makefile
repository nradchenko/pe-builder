# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile ramdrive.sys (a native NT kernel driver) for the /minint PE, using
# only a MinGW-w64 toolchain — no WDK, no MSVC (see docs/install.md for the setup).
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

# The toolchain programs (CC, WINDRES, OBJDUMP) and the VERSIONINFO attribution come from the
# shared settings, materialized here by the build; ../.shared is the by-hand fallback. It needs
# TRIPLE, so it is included after the switch above.
include $(firstword $(wildcard pebuild/common.mk ../.shared/common.mk))
ifndef PEBUILD_COMMON
$(error no common.mk at pebuild/common.mk or ../.shared/common.mk)
endif

TARGET  := ramdrive.sys
SRC     := ramdrive.c
VEROBJ  := version.o

# The DDK headers are found rather than hardcoded, since every toolchain keeps them
# somewhere else; DDK_INCLUDE=<dir> overrides the search. Why it is written this way:
# docs/plugins/conventions.md § Kernel drivers locate the DDK headers.
GCC_LIBDIR  := $(dir $(shell $(CC) -print-libgcc-file-name 2>/dev/null))
DDK_ROOT    := $(if $(GCC_LIBDIR),$(GCC_LIBDIR)../../../..)
DDK_INCLUDE ?= $(patsubst %/ntddk.h,%,$(firstword \
                 $(wildcard $(DDK_ROOT)/$(TRIPLE)/include/ddk/ntddk.h $(DDK_ROOT)/include/ddk/ntddk.h) \
                 /usr/$(TRIPLE)/include/ddk/ntddk.h))

CFLAGS  := -ffreestanding $(ARCHDEF) -O2 -Wall -Wextra -Wno-multichar \
           -fno-asynchronous-unwind-tables \
           -I'$(DDK_INCLUDE)'

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

# A driver is VFT_DRV rather than VFT_APP, and names the kind of driver it is in
# FILESUBTYPE — VFT2_DRV_SYSTEM, the catch-all for one that is not a printer, keyboard
# or other device class Windows enumerates by name.
$(VEROBJ): $(VER_RC)
	@$(call verinfo,verinfo.h,RAM disk driver,ramdrive,ramdrive.sys,VFT_DRV,VFT2_DRV_SYSTEM)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo.h\"' $< -o $@

$(TARGET): $(SRC) $(VEROBJ)
	@test -f '$(DDK_INCLUDE)/ntddk.h' || { echo "ERROR: no DDK headers at '$(DDK_INCLUDE)' —" \
	    "install the MinGW-w64 DDK headers, or set DDK_INCLUDE to the directory holding ntddk.h"; exit 1; }
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ $(SRC) $(VEROBJ) $(LDLIBS)
	@sum=`$(OBJDUMP) -p $@ 2>/dev/null | awk '/CheckSum/ { print $$2; exit }'`; \
	    test -n "$$sum" || { echo "ERROR: $(OBJDUMP) reported no PE checksum for $@ — the" \
	        "zero-checksum guard below cannot run; install the binutils for $(TRIPLE), or set OBJDUMP"; \
	        exit 1; }; \
	    case "$$sum" in *[!0]*) ;; *) echo "ERROR: $@ has a zero PE checksum — the driver will not" \
	        "load; update binutils (link-time checksum) or add a checksum step"; exit 1;; esac

clean:
	rm -f $(TARGET) $(VEROBJ) verinfo.h