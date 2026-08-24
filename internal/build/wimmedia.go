// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nradchenko/pe-builder/internal/receipt"
)

// Shared helpers for the WIM media tail (wimcd.go). The boot chain and its El Torito
// image are staged by a boot-chain plugin into a reserved subtree the tail relocates to the
// media root, and the media is mastered with names left verbatim (unlike the classic ISO,
// which uppercases the whole tree) — so lookups here are case-insensitive.

const (
	// wimBootReservedDir is the reserved subtree at the assembled-tree root that the boot-chain
	// plugin stages the ISO-media boot chain into (grldr and the El Torito boot image). The
	// media tail relocates it to the media root before capturing the WIM, so the boot chain
	// lands on the ISO filesystem rather than inside the WIM.
	wimBootReservedDir = "_wimboot"
	// wimBootImage is the grub4dos El Torito no-emulation boot image, staged at the media root.
	wimBootImage = "Boot-NoEmul.img"
)

// copyReceiptToMedia places the build receipt at the media root. The WIM media tail packs the
// assembled tree — which Build wrote the receipt into — inside the WIM, so a mastered WIM image
// carries the receipt only inside the WIM: a booted PE reads it there through the overlay, but
// `pebuild verify`, reading the image root from outside, cannot. Copying it onto the media root
// makes verify (and any receipt reader) work on a WIM image exactly as on a classic one, where
// the receipt sits at the ISO root because the tree is mastered directly. A tree with no receipt
// (a direct emit caller that skipped the receipt stage) is left alone rather than failed.
func copyReceiptToMedia(treeDir, mediaDir string) error {
	src := filepath.Join(treeDir, receipt.FileName)
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil // no receipt to surface (a direct caller skipped the receipt stage)
	} else if err != nil {
		return err
	}
	return copyFile(src, filepath.Join(mediaDir, receipt.FileName))
}

// findCaseInsensitive returns the path of the entry in dir whose name equals name ignoring
// case. The media tail masters names verbatim, so a file staged by a plugin (or the source)
// may land under either case; callers resolve it without assuming one.
func findCaseInsensitive(dir, name string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", name, dir)
}

// containsFold reports whether names contains target, comparing case-insensitively —
// plugin names resolve case-insensitively, so the implicit-enable seed must match that.
func containsFold(names []string, target string) bool {
	for _, n := range names {
		if strings.EqualFold(n, target) {
			return true
		}
	}
	return false
}
