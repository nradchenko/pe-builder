// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	godisk "github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"

	"github.com/nradchenko/pe-builder/boot"
)

// isoBlockSize is the ISO9660 logical block size. XP's CD boot chain (and El Torito
// no-emulation with a 2 KB load size) assumes 2048-byte sectors.
const isoBlockSize = 2048

// pvdSector is the sector of the primary volume descriptor, whose "volume space size"
// field gives the real image length (read by the test to confirm the image is not padded).
const pvdSector = 16

// initialSize is the disk size passed to Create — just go-diskfs's structural minimum
// (the 16-sector system area plus the volume descriptors). Finalize grows the backing
// as it writes the tree and sizes the image exactly to its volume-space-size, so a real
// build (tens of MB) overshoots this floor immediately; keeping the floor small means
// even a tiny image is grown to its true size rather than padded out to a large Create.
const initialSize = 32 * isoBlockSize

// iso masters the assembled tree into an El Torito no-emulation bootable ISO —
// the target for output.target = "iso". It reuses the source ISO's own CDBOOT
// image (bootsect.bin, placed at the tree root by the boot stage), so the on-disc
// boot chain is Microsoft's own, unmodified.
//
// go-diskfs writes full-length names (UntranslatedFilenames — no 8.3 truncation, no
// ";1" version suffix) in ECMA-119 §9.3 record order (SortDirectoryRecords), which the
// Microsoft real-mode boot reader requires. Emit uppercases the tree first, since that
// reader cannot read lowercase identifiers; keeping full length lets long names — the
// shell's WinSxS side-by-side assemblies — survive verbatim rather than colliding when
// truncated to 8.3.
type iso struct{}

func (iso) Name() string { return "iso" }

// Emit masters treeDir into a bootable ISO at opts.OutputPath.
//
// It uses treeDir directly as go-diskfs's workspace, which go-diskfs walks and
// then removes: mastering consumes the tree directory. The caller owns the tree's
// lifecycle (it is scratch for an ISO build) and must copy it first if it needs to
// survive. The tree must already contain bootsect.bin at its root; Emit uppercases the
// tree in place (uppercaseTree) so the on-disc names are boot-readable.
//
// The image is mastered into a scratch file beside opts.OutputPath and renamed onto it
// only when complete, so the output path is never a partially written ISO.
func (iso) Emit(ctx context.Context, treeDir string, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(treeDir, boot.BootSectName)); err != nil {
		return fmt.Errorf("iso: boot image %s missing from tree: %w", boot.BootSectName, err)
	}

	// UntranslatedFilenames preserves each name's case verbatim, but ISO9660 primary
	// identifiers must be uppercase d-characters — Microsoft's real-mode CDBOOT/SETUPLDR
	// cannot read lowercase names. Uppercase the whole tree so the on-disc names are
	// valid (and boot-readable) while keeping their full length (no 8.3 truncation).
	if err := uppercaseTree(treeDir); err != nil {
		return fmt.Errorf("iso: uppercase tree: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return fmt.Errorf("iso: create output dir: %w", err)
	}

	// Master into a scratch file beside the destination and rename it into place only
	// once the image is complete, so the output path holds either the previous image or
	// the new one and never a half-written master. Writing straight to it would leave a
	// truncated file — indistinguishable from a good ISO — behind any failure, and behind
	// a kill at any point during the minutes mastering takes. The cost is space: the
	// destination is replaced only at the rename, so a rebuild onto an existing image
	// needs room for both at once.
	tmp, err := reserveTemp(opts.OutputPath)
	if err != nil {
		return fmt.Errorf("iso: reserve scratch image: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmp)
		}
	}()

	disk, err := diskfs.Create(tmp, initialSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("iso: create image: %w", err)
	}
	closed := false
	defer func() {
		// Close zeroes the disk, so it must not run twice.
		if !closed {
			_ = disk.Close()
		}
	}()
	disk.LogicalBlocksize = isoBlockSize

	fsys, err := disk.CreateFilesystem(godisk.FilesystemSpec{
		Partition: 0,
		FSType:    filesystem.TypeISO9660,
		WorkDir:   treeDir,
	})
	if err != nil {
		return fmt.Errorf("iso: create filesystem: %w", err)
	}
	isofs, ok := fsys.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("iso: unexpected filesystem type %T", fsys)
	}

	// El Torito no-emulation, BIOS platform, 2 KB load size (4 × 512), with the boot
	// image and catalog hidden from the file tree — the reference recipe.
	entry := &iso9660.ElToritoEntry{
		Platform:     iso9660.BIOS,
		Emulation:    iso9660.NoEmulation,
		BootFile:     "/" + strings.ToUpper(boot.BootSectName),
		HideBootFile: true,
	}
	entry.SetLoadSize(4)
	et := &iso9660.ElTorito{
		BootCatalog:     "/boot.catalog",
		HideBootCatalog: true,
		Entries:         []*iso9660.ElToritoEntry{entry},
	}

	if err := isofs.Finalize(iso9660.FinalizeOptions{
		ElTorito:              et,
		VolumeIdentifier:      opts.VolumeLabel,
		UntranslatedFilenames: true, // verbatim long, mixed-case names (mkisofs -U); no 8.3, no ;1
		SortDirectoryRecords:  true, // ECMA-119 §9.3 order, which SETUPLDR's reader requires
	}); err != nil {
		return fmt.Errorf("iso: finalize: %w", err)
	}

	// Release the backing file before the rename: the image is only complete once its
	// last write is flushed out of the handle, and a platform whose rename cannot
	// replace an open file would fail here rather than publish a partial image.
	closed = true
	if err := disk.Close(); err != nil {
		return fmt.Errorf("iso: close image: %w", err)
	}
	if err := os.Rename(tmp, opts.OutputPath); err != nil {
		return fmt.Errorf("iso: publish image: %w", err)
	}
	published = true
	return nil
}

// reserveTemp returns an unused path for the in-progress master, in the same directory
// as dest so that publishing it is a same-filesystem os.Rename — atomic, where a copy
// across filesystems would reintroduce the partial file it exists to prevent.
//
// The name is reserved by creating the file and then removing it, because go-diskfs's
// Create opens O_EXCL and so insists on making the file itself; the window between the
// two is guarded by the random name rather than by the file existing.
func reserveTemp(dest string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".tmp-*")
	if err != nil {
		return "", err
	}
	// Past this point the file exists under a name only this function knows, so every
	// failure has to unlink it itself — Emit's deferred cleanup has no name to remove.
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

// uppercaseTree renames every file and directory under dir to the uppercase of its
// name, deepest paths first so a parent rename never invalidates a not-yet-renamed
// child path. The tree is scratch (mastering consumes it), so renaming in place is
// safe. This makes the on-disc ISO9660 identifiers valid uppercase d-characters —
// which the boot chain requires — while UntranslatedFilenames keeps them full-length.
func uppercaseTree(dir string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Guard against a case-collision: two entries whose fully-uppercased paths are
	// identical (e.g. an a/b.dll copy and an a/B.DLL [SourcePaths] destination) would
	// have one silently overwrite the other on rename. Windows sources cannot hold such
	// a pair, but a plugin could author one; surface it rather than lose a file.
	seen := make(map[string]string, len(paths))
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		up := strings.ToUpper(rel)
		if prev, ok := seen[up]; ok {
			return fmt.Errorf("names collide when uppercased for the ISO: %q and %q", prev, rel)
		}
		seen[up] = rel
	}
	// Deepest first: more path separators = deeper, so a child renames before its parent.
	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) > strings.Count(paths[j], string(os.PathSeparator))
	})
	for _, p := range paths {
		up := filepath.Join(filepath.Dir(p), strings.ToUpper(filepath.Base(p)))
		if up != p {
			if err := os.Rename(p, up); err != nil {
				return err
			}
		}
	}
	return nil
}

func init() { Register(iso{}) }
