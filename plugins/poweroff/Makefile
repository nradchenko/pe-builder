# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile poweroff.exe for the /minint PE. Needs a MinGW-w64 toolchain
# (see docs/install.md for the per-platform setup).
#
# ARCH selects the target architecture (i386 or amd64), defaulting to i386; the plugin's
# [Build.<arch>] Command passes it. This is a user-mode binary, so the architecture
# changes nothing but the toolchain.
ARCH ?= i386

ifeq ($(ARCH),amd64)
  TRIPLE := x86_64-w64-mingw32
else ifeq ($(ARCH),i386)
  TRIPLE := i686-w64-mingw32
else
  $(error unknown ARCH '$(ARCH)' — expected i386 or amd64)
endif

CC      := $(TRIPLE)-gcc
CFLAGS  := -O2 -s -Wall -Wextra
# -Wl,--no-insert-timestamp zeroes the PE TimeDateStamp so the output is byte-identical
# across builds of the same source (reproducibility).
LDFLAGS := -Wl,--no-insert-timestamp
LDLIBS  := -ladvapi32
TARGET  := poweroff.exe
SRC     := poweroff.c

.PHONY: all clean

all: $(TARGET)

$(TARGET): $(SRC)
	$(CC) $(CFLAGS) $(LDFLAGS) -o $@ $< $(LDLIBS)

clean:
	rm -f $(TARGET)