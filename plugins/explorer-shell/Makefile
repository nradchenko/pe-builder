# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the Explorer-shell binaries (32-bit Windows PE):
#   peshell.exe — the Setup\CmdLine launcher
#   peshut.dll  — the ExitWindowsEx power-off hook (loaded via AppInit_DLLs)
# Requires the MinGW-w64 i686 toolchain:  apt install gcc-mingw-w64-i686

CC      := i686-w64-mingw32-gcc
# -Wl,--no-insert-timestamp zeroes the PE TimeDateStamp for reproducible output.
LDFLAGS := -Wl,--no-insert-timestamp

.PHONY: all clean

all: peshell.exe peshut.dll

peshell.exe: peshell.c startmenu.c startmenu.h
	$(CC) -O2 -s -mwindows -Wall -Wextra $(LDFLAGS) -o $@ peshell.c startmenu.c -lole32 -luuid -lshell32 -ladvapi32 -lversion

peshut.dll: peshut.c
	$(CC) -shared -O2 -s -Wall -Wextra $(LDFLAGS) -o $@ $< -ladvapi32

clean:
	rm -f peshell.exe peshut.dll
