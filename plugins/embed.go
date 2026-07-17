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
//go:embed */*.inf
//go:embed poweroff/poweroff.c poweroff/Makefile
//go:embed ramdrive/ramdrive.c ramdrive/Makefile
//go:embed explorer-shell/peshell.c explorer-shell/peshut.c explorer-shell/startmenu.c explorer-shell/startmenu.h explorer-shell/Makefile
//go:embed pesuite/pesuite.c pesuite/Makefile
//go:embed win32-gui/rundll32.exe.manifest win32-gui/explorer.exe.manifest
var FS embed.FS
