// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package plugins embeds pe-builder's built-in plugins — the small, curated set it
// ships and can enable by name. Each top-level directory is one plugin (its .inf plus
// any source or support files); a build-capable plugin like poweroff ships source and
// a [Build] declaration, not a prebuilt binary. See docs/plugins/inclusion-policy.md
// for what may be bundled.
package plugins

import "embed"

// FS holds the built-in plugin directories. Every plugin's .inf is embedded by one glob,
// so adding a declarative plugin needs no change here. A build-capable plugin's source (its
// .c and Makefile) is listed explicitly instead of embedding the whole directory, so a
// binary a local `make` leaves in the dir can never be bundled — built-ins ship source only.
//
// .shared is not a plugin: it carries the build-support files the engine materializes into
// every plugin's build directory (see plugin.writeBuildSupport), which is how one VERSIONINFO
// template and one set of shared make settings serve them all. Discovery already skips it for
// holding no .inf; the leading dot says so at a glance, and marks it as not a name anyone
// should add an .inf to. It is embedded here, among the plugins, so that it stays inside the
// tree a build receipt hashes: a change to it changes every plugin binary and must change the
// engine hash too. Their paths are named in full rather than as a directory — either form
// works, since the rule that excludes dot-prefixed entries applies to what a directory pattern
// walks and not to the directory a pattern names, but naming each file keeps the directive
// honest about what ships.
//
// Keep every //go:embed line contiguous and last in this comment: a prose line between two of
// them is what gofmt reflows, so a comment inserted mid-block moves on the next `make fmt`.
//
//go:embed */*.inf
//go:embed poweroff/poweroff.c poweroff/Makefile
//go:embed ramdrive/ramdrive.c ramdrive/Makefile
//go:embed explorer-shell/peshell.c explorer-shell/peshut.c explorer-shell/startmenu.c explorer-shell/startmenu.h explorer-shell/Makefile
//go:embed pesuite/pesuite.c pesuite/Makefile
//go:embed startup/perun.c startup/perun.h startup/perundll.c startup/perunmain.c startup/Makefile
//go:embed hardware/pehw.c hardware/Makefile
//go:embed .shared/version.rc .shared/common.mk
//go:embed win32-gui/rundll32.exe.manifest win32-gui/explorer.exe.manifest win32-gui/rundll32.amd64.manifest win32-gui/explorer.amd64.manifest
var FS embed.FS
