// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"strings"

	"github.com/nradchenko/pe-builder/source"
)

// wimImageFile is the payload WIM's name at a wim-layout image's media root (the build
// writes it as build.wimCDImageName). Its presence at the root is what marks an image as
// wim-layout: the Windows file tree and the registry hives live inside it, not on the ISO
// filesystem that the image and hive commands read.
const wimImageFile = "PE.wim"

// isWIMLayoutImage reports whether b is a wim-layout image — one whose payload is sealed
// inside PE.wim at the root rather than laid out as a file tree. Detection is the WIM's
// presence at the root, matched case-insensitively because the ISO stage may uppercase it.
func isWIMLayoutImage(b source.Backing) bool {
	ents, err := fs.ReadDir(b, ".")
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(e.Name(), wimImageFile) {
			return true
		}
	}
	return false
}

// wimImageHint is the fallback guidance shown when a wim-layout image's payload could not be
// read through PE.wim (a corrupt or unsupported WIM): the image and hive commands normally read
// into it transparently, so this appears only when that fails. It is appended to their
// "not found" messages rather than turning them into a hard error, so the media-root files (the
// WIM itself, the boot core) stay listable and extractable.
const wimImageHint = "this is a WIM-layout image and the payload inside " + wimImageFile +
	" could not be read — extract it with `pebuild image extract <image> " + wimImageFile +
	"` and inspect it with a WIM tool, or build the manifest with the classic layout to inspect " +
	"the tree directly"
