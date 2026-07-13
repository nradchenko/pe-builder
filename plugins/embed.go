// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package plugins embeds pe-builder's built-in plugins — the small, curated set it
// ships and can enable by name. Each top-level directory is one plugin (its .inf plus
// any source or support files); a build-capable plugin like poweroff ships source and
// a [Build] declaration, not a prebuilt binary. See docs/plugins/inclusion-policy.md
// for what may be bundled.
package plugins

import "embed"

// FS holds the built-in plugin directories, each a top-level entry. Built-in plugins
// are embedded by their source files explicitly (not whole directories), so a binary
// left in a plugin dir by a local `make` can never be bundled — built-in plugins ship
// source only.
//
//go:embed poweroff/poweroff.inf poweroff/poweroff.c poweroff/Makefile
//go:embed win32-gui/win32-gui.inf
//go:embed storage-pnp/storage-pnp.inf
var FS embed.FS
