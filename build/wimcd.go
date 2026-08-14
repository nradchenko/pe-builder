// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nradchenko/pe-builder/internal/coreimg"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/source"
	"github.com/nradchenko/pe-builder/target"
)

// wimCDBootPlugin is the built-in meta-plugin the wim-cd layout enables implicitly. Its
// depends pull in the grub4dos loader, the firadisk reclaim driver, and the wimmf overlay,
// and it declares the fltmgr/wimmf boot-start registry contract the overlay needs.
const wimCDBootPlugin = "wim-cd-boot"

const (
	// coreImgName is the boot-core image at the media root: the pre-wimmf boot closure
	// mastered as a bootable cdfs and gzip-compressed. The pre-loader maps it into RAM as a
	// CD; the name is referenced by the generated menu.lst.
	coreImgName = "coreimg.gz"
	// menuLstName is grub4dos's config file, read at the media root by grldr.
	menuLstName = "menu.lst"
	// wimCDImageName is the payload WIM, placed at the media root. The media is mastered by
	// MasterElTorito directly, which writes names verbatim (no uppercasing — that is the iso
	// target's Emit, which this path does not use), so the file lands on the disc as PE.wim.
	// This chain reaches the WIM only through wimmf's WimPath, so it is free to sit at the root
	// under a short name. The WimPath the wim-cd-boot plugin sets is uppercase
	// (\Device\CdRom0\PE.WIM); it resolves this file because cdfs and wimmf match the path
	// suffix case-insensitively, and wimmf resolves the CD drive itself.
	wimCDImageName = "PE.wim"
)

// menuLstFor builds the grub4dos boot script the wim-cd tail writes to the media root. grldr
// reads it, maps the boot-core image (coreName) into extended memory as an INT13h CD (drive
// 0xff), hooks the mapping, and chainloads it — an unmodified setupldr then boots /minint off
// that RAM CD. A gzip-compressed image is decompressed by map --mem into RAM transparently.
// The sequence is map --mem / map --hook / chainloader / boot.
func menuLstFor(coreName string) string {
	return `default 0
timeout 0

title Windows PE
map --mem /` + coreName + ` (0xff)
map --hook
chainloader (0xff)
boot
`
}

// emitWIMCDISO is the media tail for the WIM layout (layout = "wim"): the distributable WIM boot
// chain. It masters the pre-wimmf boot closure into a small bootable cdfs image (coreimg) that
// grub4dos maps into RAM as a CD and boots with an unmodified setupldr; firadisk reclaims that
// RAM region and the wimmf overlay serves the rest of the tree from the compressed WIM on the
// real CD. It ships no Microsoft boot binary. (The CD boot device is this chain's; a WIM boot
// for a different device would be a sibling media tail selected by its own boot-chain plugin.)
//
// The boot chain — grldr and its El Torito no-emulation image — is staged by the boot-chain
// plugin into a reserved subtree (wimBootReservedDir); this relocates that subtree to the media
// root, generates the menu.lst that drives the chain, builds coreimg from the boot closure the
// assemble stage resolved (rep.CoreMembers), captures the payload into the WIM beside it, and
// masters the grldr-chained ISO.
func emitWIMCDISO(ctx context.Context, m *manifest.Manifest, treeDir string, rep *Report) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// The boot closure must have been resolved (assemble does this for the wim-cd layout);
	// without it there is no coreimg to build, so fail clearly rather than master an
	// unbootable ISO.
	if len(rep.CoreMembers) == 0 {
		return fmt.Errorf("layout %q: boot core is empty (no members resolved)", manifest.LayoutWIM)
	}

	// The boot-chain plugin must have staged the reserved subtree (grldr + the El Torito
	// image); without it there is no loader to boot.
	reserved := filepath.Join(treeDir, wimBootReservedDir)
	if _, err := os.Stat(reserved); err != nil {
		return fmt.Errorf("layout %q: boot chain not staged at %s (enable the %q plugin): %w",
			manifest.LayoutWIM, wimBootReservedDir, wimCDBootPlugin, err)
	}

	// Relocate the reserved subtree out of the payload to the ISO media root, so the boot
	// chain lands on the ISO filesystem and not inside the WIM.
	mediaDir, err := os.MkdirTemp(filepath.Dir(treeDir), "wimcd-media-")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(mediaDir); err != nil { // take the unique name for the rename
		return err
	}
	if err := os.Rename(reserved, mediaDir); err != nil {
		return fmt.Errorf("relocate boot chain: %w", err)
	}
	// mediaDir is scratch. On the default path mastering consumes it; keep_work preserves it
	// for inspection. Remove it on any earlier error so a failed build leaves no orphan.
	defer func() {
		if !m.Build.KeepWork {
			_ = os.RemoveAll(mediaDir)
		}
	}()

	// Write the grub4dos boot script the loader reads.
	if err := os.WriteFile(filepath.Join(mediaDir, menuLstName), []byte(menuLstFor(coreImgName)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", menuLstName, err)
	}

	// Build the boot-core image from the resolved closure and the source's own CDBOOT, and
	// place it at the media root where menu.lst maps it. The source must be an ISO for the
	// CDBOOT extraction (as the classic tail also requires).
	srcISO, err := source.OpenImageFile(m.Source.Path)
	if err != nil {
		return err
	}
	err = coreimg.Build(ctx, treeDir, rep.CoreMembers, srcISO, coreimg.Options{
		OutputPath:  filepath.Join(mediaDir, coreImgName),
		VolumeLabel: m.Output.VolumeLabel,
	})
	_ = srcISO.Close()
	if err != nil {
		return fmt.Errorf("build boot core: %w", err)
	}

	// Capture the payload tree into the WIM, placed at the media root beside the boot core
	// (mediaDir already exists, so no directory needs creating). The image's architecture and
	// version are taken from the probed source (rep.Identity) rather than assumed.
	wimPath := filepath.Join(mediaDir, wimCDImageName)
	if err := captureWIM(ctx, treeDir, wimPath, rep.Identity); err != nil {
		return err
	}

	// Surface the build receipt on the media root so `pebuild verify` can read it from the ISO
	// (the tree's copy is now sealed inside the WIM).
	if err := copyReceiptToMedia(treeDir, mediaDir); err != nil {
		return fmt.Errorf("place receipt on media: %w", err)
	}

	// Resolve the El Torito boot image case-insensitively; the boot catalog references it by
	// extent, so its on-disc name only needs to match the path passed to the master step.
	bootImg, err := findCaseInsensitive(mediaDir, wimBootImage)
	if err != nil {
		return fmt.Errorf("locate boot image: %w", err)
	}
	bootFile := "/" + filepath.Base(bootImg)

	// Master the media root into the grldr-chained ISO; mastering consumes its workspace.
	// With keep_work, master a clone so the media root (and the WIM/coreimg in it) survive
	// for inspection alongside the payload tree — mirroring the classic and wim tails.
	masterDir := mediaDir
	if m.Build.KeepWork {
		masterDir, err = cloneTree(mediaDir)
		if err != nil {
			return err
		}
		defer os.RemoveAll(masterDir)
	}
	if err := target.MasterElTorito(ctx, masterDir, target.Options{
		OutputPath:  m.Output.Path,
		VolumeLabel: m.Output.VolumeLabel,
	}, bootFile); err != nil {
		_ = os.Remove(m.Output.Path)
		return err
	}

	// The payload tree is not consumed by mastering (the media root is); drop it unless the
	// build keeps its work directory.
	if !m.Build.KeepWork {
		_ = os.RemoveAll(treeDir)
	}

	info, err := os.Stat(m.Output.Path)
	if err != nil {
		return fmt.Errorf("stat output iso: %w", err)
	}
	rep.ISOPath = m.Output.Path
	rep.ISOSize = info.Size()
	rep.VolumeLabel = m.Output.VolumeLabel
	return nil
}
