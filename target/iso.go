// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"log/slog"
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

// pvdSector is the sector of the primary volume descriptor (its "volume space
// size" field gives the real image length, used to trim trailing padding).
const pvdSector = 16

// initialSize is the disk size passed to Create — just a floor above go-diskfs's
// minimum (system area + volume descriptors). The backing grows as Finalize writes
// the tree (WriteAt extends it) and trim shrinks it to the real length afterward,
// so it need not fit the tree and no tree walk is spent estimating it.
const initialSize = 1 << 20

// iso masters the assembled tree into an El Torito no-emulation bootable ISO —
// the target for output.target = "iso". It reuses the source ISO's own CDBOOT
// image (bootsect.bin, placed at the tree root by the boot stage), so the on-disc
// boot chain is Microsoft's own, unmodified.
//
// go-diskfs writes plain ISO9660 (level 1): on-disc names are uppercased and
// truncated to 8.3, which is exactly what the boot chain expects (\I386\SETUPLDR.BIN
// and the core boot set are all 8.3). It does not offer the reference recipe's
// level-4 long names, so a future file set with long names that must survive
// verbatim (or that collide within 8.3) would need a different writer.
type iso struct{}

func (iso) Name() string { return "iso" }

// Emit masters treeDir into a bootable ISO at opts.OutputPath.
//
// It uses treeDir directly as go-diskfs's workspace, which go-diskfs walks and
// then removes: mastering consumes the tree directory. The caller owns the tree's
// lifecycle (it is scratch for an ISO build) and must copy it first if it needs to
// survive. The tree must already contain bootsect.bin at its root and the
// uppercase-resolvable \I386 boot files; go-diskfs uppercases on-disc names, so the
// tree itself stays in its canonical case.
func (iso) Emit(ctx context.Context, treeDir string, opts Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(treeDir, boot.BootSectName)); err != nil {
		return fmt.Errorf("iso: boot image %s missing from tree: %w", boot.BootSectName, err)
	}

	// go-diskfs writes 8.3 names; a name it would truncate or alter would be
	// unfindable by the boot chain that looks it up verbatim. Warn so the divergence
	// is visible rather than a silently non-booting image.
	if err := warnUnmappableNames(treeDir); err != nil {
		return fmt.Errorf("iso: scan names: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return fmt.Errorf("iso: create output dir: %w", err)
	}
	// go-diskfs Create refuses to overwrite (O_EXCL); clear a stale artifact first.
	_ = os.Remove(opts.OutputPath)
	disk, err := diskfs.Create(opts.OutputPath, initialSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("iso: create image: %w", err)
	}
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
		BootFile:     "/" + boot.BootSectName,
		HideBootFile: true,
	}
	entry.SetLoadSize(4)
	et := &iso9660.ElTorito{
		BootCatalog:     "/boot.catalog",
		HideBootCatalog: true,
		Entries:         []*iso9660.ElToritoEntry{entry},
	}

	if err := isofs.Finalize(iso9660.FinalizeOptions{
		ElTorito:         et,
		VolumeIdentifier: opts.VolumeLabel,
	}); err != nil {
		return fmt.Errorf("iso: finalize: %w", err)
	}

	// go-diskfs's directory records need two fixups before the Microsoft boot chain can
	// read them: the ";1" version suffixes stripped (CDBOOT matches \I386\SETUPLDR.BIN
	// without a version), and the records within each directory re-sorted into ISO9660
	// §9.3 order (SETUPLDR's real-mode reader requires it to find NTDETECT and the rest
	// of \I386). A single walk of the directory tree does both.
	if err := normalizeDirectories(opts.OutputPath); err != nil {
		return fmt.Errorf("iso: normalize directory records: %w", err)
	}

	// The backing grew past initialSize as Finalize wrote; trim it to the real image
	// length so the artifact isn't padded with trailing zero sectors. A trim failure
	// is not fatal — the (merely padded) ISO is already written and bootable — so it
	// is logged rather than discarding a finished image.
	if err := trim(opts.OutputPath); err != nil {
		slog.Warn("could not trim ISO padding; the image is valid but larger than necessary", "err", err)
	}
	return nil
}

// dirRecord field offsets within an ISO9660 directory record.
const (
	drExtentLBA = 2  // little-endian extent location (blocks)
	drDataLen   = 10 // little-endian data length (bytes)
	drFlags     = 25 // file flags; bit 1 marks a directory
	drNameLen   = 32 // length of the file identifier
	drName      = 33 // the file identifier itself
	drFlagDir   = 0x02
)

// normalizeDirectories rewrites the ISO's directory tree so the Microsoft boot chain
// can read it, in a single BFS walk that does two things to every directory extent:
//
//   - Strips the ISO9660 ";N" version suffix from each file name (shortening the
//     record's name-length field to end at the ";", leaving the record length and the
//     now-unused suffix bytes in place). CDBOOT matches \I386\SETUPLDR.BIN without a
//     version and fails to find it otherwise. go-diskfs also renders an extensionless
//     name as "NAME." (a bare trailing dot before ";N"); that dot is dropped too, so
//     the on-disc name is exactly "NAME".
//   - Re-sorts the records into ISO9660 §9.3 order (file identifiers ascending, with
//     "." and ".." kept first). go-diskfs emits them in an arbitrary order, but XP's
//     SETUPLDR real-mode reader requires §9.3 order to locate a file, so an unsorted
//     \I386 leaves it unable to find NTDETECT.COM and the boot loops. (CDBOOT
//     linear-scans, so it still finds SETUPLDR.BIN — which is why the failure only
//     surfaces after the boot sector hands off.) The sort runs after the version strip
//     so records sort by their final on-disc names.
//
// Each directory's data length is rewritten to its block-aligned size — the block
// count go-diskfs already allocated for the extent — so no extent moves, the PVD
// volume space size is unchanged, and trim still applies. That size is stored in four
// places (a directory's own "." record, each child's ".." record, the child's record
// in its parent, and the PVD root record); each is the block-aligned form of a length
// already present in a record, so the walk fixes them locally without a cross-extent
// size table.
func normalizeDirectories(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	pvd, rootLBA, rootSize, err := readPVD(f)
	if err != nil {
		return err
	}

	visited := map[uint32]bool{}
	queue := [][2]uint32{{rootLBA, rootSize}}
	for len(queue) > 0 {
		lba, size := queue[0][0], queue[0][1]
		queue = queue[1:]
		if visited[lba] {
			continue
		}
		visited[lba] = true

		buf, err := readExtent(f, lba, size)
		if err != nil {
			return err
		}
		recs := dirRecords(buf, size)

		// First fix every record's name and child sizes, then sort — so the sort keys
		// off the stripped names.
		for _, rec := range recs {
			nameLen := int(rec[drNameLen])
			name := rec[drName : drName+nameLen]
			isDot := nameLen == 1 && (name[0] == 0x00 || name[0] == 0x01)
			if rec[drFlags]&drFlagDir != 0 {
				if !isDot {
					// A child directory: descend, and set its record's size to the
					// child extent's block-aligned length.
					childLBA := binary.LittleEndian.Uint32(rec[drExtentLBA:])
					childSize := binary.LittleEndian.Uint32(rec[drDataLen:])
					queue = append(queue, [2]uint32{childLBA, childSize})
					setDataLen(rec, align(childSize))
				}
			} else if i := bytes.IndexByte(name, ';'); i >= 0 {
				if i > 0 && name[i-1] == '.' {
					i--
				}
				rec[drNameLen] = byte(i)
			}
		}
		// recs[0]="." (this directory) and recs[1]=".." (its parent) carry the self and
		// parent sizes; block-align both. Every other record keeps its position relative
		// to these two, which stay first.
		setDataLen(recs[0], align(size))
		setDataLen(recs[1], align(binary.LittleEndian.Uint32(recs[1][drDataLen:])))

		// §9.3: order the remaining records by file-identifier bytes ascending. Plain
		// byte comparison matches ECMA-119's space-padded compare here — the pad 0x20
		// sorts below every d-character, ".", and ";".
		tail := recs[2:]
		sort.Slice(tail, func(i, j int) bool {
			ni, nj := int(tail[i][drNameLen]), int(tail[j][drNameLen])
			return bytes.Compare(tail[i][drName:drName+ni], tail[j][drName:drName+nj]) < 0
		})

		packed, err := packRecords(recs, int(align(size)))
		if err != nil {
			return err
		}
		if _, err := f.WriteAt(packed, int64(lba)*isoBlockSize); err != nil {
			return err
		}
	}

	// The PVD's own copy of the root directory record carries the root extent's size.
	setDataLen(pvd[156:], align(rootSize))
	if _, err := f.WriteAt(pvd, pvdSector*isoBlockSize); err != nil {
		return err
	}
	return nil
}

// align rounds a directory size up to a whole number of logical blocks.
func align(size uint32) uint32 {
	return ((size + isoBlockSize - 1) / isoBlockSize) * isoBlockSize
}

// readPVD reads and validates the primary volume descriptor, returning it along with
// the root directory record's extent location and size (the root record sits at
// offset 156 of the PVD).
func readPVD(f *os.File) (pvd []byte, rootLBA, rootSize uint32, err error) {
	pvd = make([]byte, isoBlockSize)
	if _, err = f.ReadAt(pvd, pvdSector*isoBlockSize); err != nil {
		return nil, 0, 0, err
	}
	if pvd[0] != 0x01 || string(pvd[1:6]) != "CD001" {
		return nil, 0, 0, fmt.Errorf("no primary volume descriptor at sector %d", pvdSector)
	}
	rootLBA = binary.LittleEndian.Uint32(pvd[156+drExtentLBA:])
	rootSize = binary.LittleEndian.Uint32(pvd[156+drDataLen:])
	return pvd, rootLBA, rootSize, nil
}

// readExtent reads a directory extent — size rounded up to whole logical blocks —
// starting at the given block address.
func readExtent(f *os.File, lba, size uint32) ([]byte, error) {
	blocks := (size + isoBlockSize - 1) / isoBlockSize
	buf := make([]byte, blocks*isoBlockSize)
	if _, err := f.ReadAt(buf, int64(lba)*isoBlockSize); err != nil {
		return nil, err
	}
	return buf, nil
}

// dirRecords returns each directory record within the first size bytes of a
// directory extent as a sub-slice of buf (so edits write through), skipping the
// zero padding that fills out each logical block.
func dirRecords(buf []byte, size uint32) [][]byte {
	var recs [][]byte
	for off := 0; off < int(size); {
		recLen := int(buf[off])
		if recLen == 0 {
			// Records do not span logical blocks; skip the block's zero padding.
			off = (off/isoBlockSize + 1) * isoBlockSize
			continue
		}
		recs = append(recs, buf[off:off+recLen])
		off += recLen
	}
	return recs
}

// setDataLen writes an ISO9660 directory record's data-length field, which is
// stored in both byte orders: little-endian at offset 10, big-endian at offset 14.
func setDataLen(rec []byte, size uint32) {
	binary.LittleEndian.PutUint32(rec[drDataLen:], size)
	binary.BigEndian.PutUint32(rec[drDataLen+4:], size)
}

// packRecords lays records back into a directory extent of capacity bytes, keeping
// the ISO9660 rule that no record spans a logical block (padding to the next block
// when one would), and zero-filling the remainder. It errors if the records do not
// fit — which cannot happen while the block count is preserved, but guards against a
// silently truncated directory.
func packRecords(recs [][]byte, capacity int) ([]byte, error) {
	out := make([]byte, capacity)
	pos := 0
	for _, rec := range recs {
		if pos%isoBlockSize != 0 && pos%isoBlockSize+len(rec) > isoBlockSize {
			pos = (pos/isoBlockSize + 1) * isoBlockSize
		}
		if pos+len(rec) > capacity {
			return nil, fmt.Errorf("directory records do not fit in %d bytes", capacity)
		}
		copy(out[pos:], rec)
		pos += len(rec)
	}
	return out, nil
}

// warnUnmappableNames walks the tree and warns for any file or directory whose name
// go-diskfs's ISO9660 level-1 writer would not preserve verbatim (beyond
// uppercasing) — a name longer than 8.3 or carrying characters outside the
// permitted set. Such a file is renamed on disc and so unfindable by the loader,
// kernel, or a registry ImagePath that names it. It only warns: the file may not be
// boot-critical, and the whole current base set is already 8.3-clean.
func warnUnmappableNames(treeDir string) error {
	return filepath.WalkDir(treeDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == treeDir {
			return nil
		}
		if name := d.Name(); !is83Clean(name) {
			rel, _ := filepath.Rel(treeDir, p)
			slog.Warn("file name is not 8.3-clean and will be renamed on the ISO; boot lookups by this name will fail",
				"path", filepath.ToSlash(rel))
		}
		return nil
	})
}

// is83Clean reports whether name survives ISO9660 level-1 mastering unchanged apart
// from case: a base of at most 8 and a single extension of at most 3 characters,
// all drawn from letters, digits, and underscore.
func is83Clean(name string) bool {
	base, ext, hasDot := strings.Cut(name, ".")
	if hasDot && strings.Contains(ext, ".") {
		return false // a second dot becomes an illegal character
	}
	if len(base) == 0 || len(base) > 8 || len(ext) > 3 {
		return false
	}
	return isDChars(base) && isDChars(ext)
}

// isDChars reports whether s is entirely letters, digits, or underscore.
func isDChars(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

// trim shrinks the ISO file to the volume space size recorded in its primary
// volume descriptor (in logical blocks), removing the padding Create left.
func trim(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	pvd, _, _, err := readPVD(f)
	if err != nil {
		return err
	}
	// Volume space size: total logical blocks, little-endian at byte 80.
	blocks := binary.LittleEndian.Uint32(pvd[80:84])
	return f.Truncate(int64(blocks) * isoBlockSize)
}

func init() { Register(iso{}) }
