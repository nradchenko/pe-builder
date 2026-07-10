# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile poweroff.exe (32-bit Windows PE) for the /minint PE.
# Requires the MinGW-w64 i686 toolchain:  apt install gcc-mingw-w64-i686

CC      := i686-w64-mingw32-gcc
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