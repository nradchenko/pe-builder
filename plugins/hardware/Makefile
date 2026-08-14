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

.PHONY: all clean

all: pehw.exe

version.o: $(VER_RC)
	@$(call verinfo,verinfo.h,Runtime hardware and network configuration,pehw,pehw.exe,VFT_APP)
	$(WINDRES) -I. -DVER_HEADER='\"verinfo.h\"' $< -o $@

# ole32 is for the INetCfg component install (`pehw net`); netcfgx.dll itself is loaded and
# registered at run time rather than linked, so it is not named here.
pehw.exe: pehw.c version.o
	$(CC) -mconsole $(CFLAGS) $(LDFLAGS) -o $@ pehw.c version.o \
	      -lsetupapi -ladvapi32 -lole32 -lkernel32

clean:
	rm -f pehw.exe version.o verinfo.h
