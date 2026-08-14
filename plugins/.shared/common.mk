# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0
#
# Build settings every build-capable plugin shares: how the toolchain programs are found, and
# the attribution its binaries stamp into their Windows VERSIONINFO resource. A plugin includes
# this rather than restating it:
#
#     include $(firstword $(wildcard pebuild/common.mk ../.shared/common.mk))
#     ifndef PEBUILD_COMMON
#     $(error ...)
#     endif
#
# placed after the ARCH switch, which is what sets TRIPLE. The build materializes this file into
# the plugin's build directory as `pebuild/common.mk` (see plugin.writeBuildSupport); the second
# path is the in-tree fallback, for a by-hand `make` in the plugin's own directory.
#
# Why each block is written the way it is: docs/plugins/conventions.md. Keep the reasoning there
# rather than restating it per plugin.

ifndef TRIPLE
$(error common.mk: TRIPLE is not set — include this after the ARCH switch that sets it)
endif

# The sentinel a plugin tests after including this file. make ignores an `include` whose argument
# expands to empty and CC has a built-in default, so without the test a missing common.mk is not
# an error.
PEBUILD_COMMON := 1

# Delete a target whose recipe failed, so a binary that fails a post-link check — the drivers'
# zero-PE-checksum guard — is not left on disk to look up to date on the next run.
.DELETE_ON_ERROR:

# --- Toolchain programs ---------------------------------------------------------------------
#
# Each is resolved on its own: a cross-toolchain prefixes its programs with the target triple, a
# native one does not, and one toolchain may do both for different programs. The prefixed name
# wins where it exists; the plain name is taken only when the compiler on PATH targets TRIPLE, so
# a build for another architecture stops at a missing program instead.
NATIVE_TRIPLE := $(filter $(TRIPLE),$(shell gcc -dumpmachine 2>/dev/null))
find-tool      = $(if $(shell command -v $(TRIPLE)-$(1) 2>/dev/null),$(TRIPLE)-$(1),\
                   $(if $(NATIVE_TRIPLE),$(1),$(TRIPLE)-$(1)))
CC      := $(call find-tool,gcc)
WINDRES := $(call find-tool,windres)
OBJDUMP := $(call find-tool,objdump)

# The programs have to exist under whichever names find-tool settled on. This is checked here
# rather than in each plugin's [Build] Requires, which can only name a literal: which name is
# right depends on the toolchain, so a literal there refuses builds that would have worked.
# Every plugin compiles and builds a resource, so CC and WINDRES are checked; OBJDUMP is not,
# because only the drivers use it and their own checksum guard reports it missing.
#
# The message names PATH because that is the lever every caller has. A plugin's [Build] Command
# is fixed in its INF — `make ARCH=i386` — so it can pass make a variable but the person running
# `pebuild build` cannot. CC is assigned unconditionally above rather than with `?=` on purpose:
# pebuild hands the build the caller's environment, so honouring an exported CC would let a
# stray `CC=clang` compile these Windows binaries with the host compiler.
#
# Goals needing no toolchain are exempt; the filter keeps `make clean all` checking (its `all`
# needs one) and a bare `make` too, where MAKECMDGOALS is empty.
GOALS := $(or $(MAKECMDGOALS),all)
ifneq ($(filter-out clean,$(GOALS)),)
MISSING := $(strip $(foreach t,$(CC) $(WINDRES),$(if $(shell command -v $(t) 2>/dev/null),,$(t))))
ifneq ($(MISSING),)
$(error no MinGW-w64 toolchain for $(TRIPLE) on PATH — not found: $(MISSING). Install one, or \
        put it on PATH)
endif
endif

# --- Version resource -----------------------------------------------------------------------
#
# Attribution for the VERSIONINFO resource. pebuild passes all of these to the plugin's [Build];
# the defaults are what a by-hand `make` in a plugin directory gets. VERSION_NUM is the version
# reduced to the four numbers FILEVERSION takes — derived once, in Go (buildinfo.VersionQuad),
# because as shell here it drifted and got it wrong.
PEBUILD_VERSION     ?= dev
PEBUILD_VERSION_NUM ?= 0,0,0,0
PEBUILD_PRODUCT     ?= pe-builder
PEBUILD_COMPANY     ?= Nikita Radchenko
PEBUILD_COPYRIGHT   ?= Copyright 2026 Nikita Radchenko. Apache-2.0
VER_STR             := $(PEBUILD_VERSION)
VER_NUM             := $(PEBUILD_VERSION_NUM)
# The resource template is materialized alongside this file, with the same in-tree fallback.
VER_RC              := $(firstword $(wildcard pebuild/version.rc ../.shared/version.rc))

# Write the values into a header the template includes. They are not passed as windres -D defines
# because windres cannot carry a value containing a space, which the company name and every
# description do; only the header's own name is a define, a file name having none. A plugin
# building two binaries names a header per binary — one shared name would race under make -j.
#
# HASH is how a literal # reaches a variable body, where make would read it as a comment.
#
# $(call verinfo,<header>,<description>,<internal>,<original>,<file type>[,<subtype>])
HASH := \#
verinfo = printf '%s\n' \
  '$(HASH)define VER_NUM $(VER_NUM)' \
  '$(HASH)define VER_STR "$(VER_STR)"' \
  '$(HASH)define VER_PRODUCT "$(PEBUILD_PRODUCT)"' \
  '$(HASH)define VER_COMPANY "$(PEBUILD_COMPANY)"' \
  '$(HASH)define VER_COPYRIGHT "$(PEBUILD_COPYRIGHT)"' \
  '$(HASH)define VER_DESC "$(2)"' \
  '$(HASH)define VER_INTERNAL "$(3)"' \
  '$(HASH)define VER_ORIGINAL "$(4)"' \
  $(if $(5),'$(HASH)define VER_FILETYPE $(5)') \
  $(if $(6),'$(HASH)define VER_FILESUBTYPE $(6)') > $(1)
