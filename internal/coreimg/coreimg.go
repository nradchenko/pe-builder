// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package coreimg masters the pre-wimmf boot core into coreimg.gz: a gzip-compressed,
// minimal bootable cdfs image holding only the boot-core file set. The WIM-boot chain's
// pre-loader (grub4dos) maps it into RAM as an INT13h CD (map --mem) and boots it with an
// unmodified setupldr — so it is a bootable XP CD in miniature: the source's own CDBOOT
// image chainloading \I386\SETUPLDR.BIN off the RAM volume, not a bespoke format. Once the
// kernel is up, firadisk re-presents the mapped RAM region as SystemRoot and wimmf overlays
// the full tree from the compressed WIM on the real CD; this image carries only what has to
// exist before that overlay is serving (see base.ComputeBootCore).
//
// It reuses the classic iso target's exact mastering recipe (uppercase on-disc names, El
// Torito no-emulation, the unmodified Microsoft CDBOOT) so the RAM CD boots identically to a
// real source CD.
package coreimg

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/target"
)

// Options configures the coreimg build.
type Options struct {
	// OutputPath is where the image is written.
	OutputPath string
	// VolumeLabel is the ISO9660 volume identifier of the bootable image.
	VolumeLabel string
}

// Build assembles the boot-core files from srcTree into a bootable cdfs image and writes it
// gzip-compressed to opts.OutputPath.
//
// members are output-tree-relative (forward-slash) paths within srcTree — the boot closure
// base.ComputeBootCore resolves (its Staged names mapped through the copy actions to their
// placed paths) plus the SYSTEM hive. Every member must exist under srcTree: a member the
// tree never placed is a boot-start file that will be absent from the RAM CD, i.e. STOP 0x7B
// (INACCESSIBLE_BOOT_DEVICE) at boot, so a missing one is a hard error here rather than a
// silent omission. The caller is expected to pass paths it actually wrote (the intersection
// of the core set with the assembled tree).
//
// srcISO is the source installation ISO, read for its El Torito CDBOOT image (the same image
// the classic tail reuses via boot.WriteBootImage); the source must therefore be an ISO for a
// bootable core, exactly as the classic iso target requires.
func Build(ctx context.Context, srcTree string, members []string, srcISO io.ReaderAt, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("coreimg: no output path")
	}
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return err
	}

	// Stage the core subset into a scratch tree next to the output (so it shares a
	// filesystem with the eventual image and the rename in the master step is cheap).
	staging, err := os.MkdirTemp(filepath.Dir(opts.OutputPath), "coreimg-stage-")
	if err != nil {
		return err
	}
	// The iso target's Emit consumes (removes) the staging tree; this cleans it up on any
	// earlier error too, and no-ops once Emit has taken it.
	defer os.RemoveAll(staging)

	if err := stageMembers(srcTree, members, staging); err != nil {
		return err
	}

	// Place the source ISO's own CDBOOT image at the staging root; the iso target masters
	// it as the El Torito boot image, so the RAM CD's boot chain is Microsoft's, unmodified.
	if _, err := boot.WriteBootImage(srcISO, staging); err != nil {
		return err
	}

	// grub4dos map --mem gunzip corrupts the last ~2 KB of the decompressed image, so place a
	// throwaway padding file where that damage is harmless: the last content extent go-diskfs
	// writes. Extents are laid out in a depth-first walk of the tree with each directory's own
	// files written before its (name-sorted) subdirectories', so the final extent is the lone
	// file of the top-level directory whose name sorts last. A dedicated last-sorting directory
	// therefore captures the corruption unconditionally — whatever the boot-core member set or
	// the source's architecture — while a real boot file keeps its bytes intact. (go-diskfs
	// rounds the image up with a trailing zero block after the pad, so the corruptible tail is
	// the pad plus that padding — both throwaway; the coreimg test asserts exactly this.)
	if err := writeTailPad(staging); err != nil {
		return err
	}

	// Master the staging tree into a bootable ISO via the classic iso target (uppercase
	// names + El Torito no-emulation + the CDBOOT sector), then gzip that image. grub4dos
	// map --mem gunzips it back into RAM transparently, so the media ships the compressed
	// form (~2 MB) while the mapped RAM volume is the full bootable cdfs (~5 MB).
	tgt, ok := target.Get("iso")
	if !ok {
		return fmt.Errorf("coreimg: iso target not registered")
	}
	tmpISO := opts.OutputPath + ".tmp.iso"
	defer os.Remove(tmpISO)
	if err := tgt.Emit(ctx, staging, target.Options{OutputPath: tmpISO, VolumeLabel: opts.VolumeLabel}); err != nil {
		return fmt.Errorf("coreimg: master image: %w", err)
	}
	if err := gzipFile(tmpISO, opts.OutputPath); err != nil {
		return fmt.Errorf("coreimg: gzip image: %w", err)
	}
	return nil
}

// stageMembers copies each output-tree-relative member from srcTree into dst, preserving its
// directory structure. A member absent from srcTree is a hard error (see Build): it would be
// a boot-start file missing from the core.
func stageMembers(srcTree string, members []string, dst string) error {
	for _, m := range members {
		rel := filepath.FromSlash(m)
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(srcTree, rel), out); err != nil {
			return fmt.Errorf("coreimg: stage %q: %w", m, err)
		}
	}
	return nil
}

// tailPad names the throwaway padding file's directory and size. The directory name is chosen
// to sort after every real top-level entry once the iso target uppercases the tree for the disc
// (I386, and AMD64 on amd64 media) — d-characters only, no separators, so it survives the
// untranslated-name validation — so its lone file is go-diskfs's last content extent. 64 KiB
// comfortably covers the ~2 KB the grub4dos gunzip corrupts at the tail.
const (
	tailPadDir  = "ZZZZZZZZ"
	tailPadName = "PADDING.PAD"
	tailPadSize = 64 * 1024
)

// tailPadMarker fills the padding file, instead of zeros, with a recognizable self-describing
// pattern. Its length divides both tailPadSize and the 2048-byte ISO block, so the pad's final
// block is exactly this marker repeated — which lets a test confirm the pad really is the last
// content extent (the bytes the gunzip corruption lands in) by reading the image's tail, rather
// than trusting name order alone.
var tailPadMarker = []byte("PEBLDPAD")

// writeTailPad drops the tail-padding file into staging under a dedicated last-sorting
// directory (see the call site and tailPad constants for why this captures the gunzip
// corruption).
func writeTailPad(staging string) error {
	dir := filepath.Join(staging, tailPadDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	pad := bytes.Repeat(tailPadMarker, tailPadSize/len(tailPadMarker))
	if err := os.WriteFile(filepath.Join(dir, tailPadName), pad, 0o644); err != nil {
		return fmt.Errorf("coreimg: write tail padding: %w", err)
	}
	return nil
}

// copyFile copies the contents of src to dst (dst's parent must exist).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// gzipFile writes a gzip-compressed copy of src to dst.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	// On any failure past this point, remove the partial output so a truncated coreimg.gz
	// is never left behind to be mistaken for a good artifact.
	zw, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}
