// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package disk creates and opens raw disk images carrying a single FAT volume — the
// scratch storage a PE is booted with when the question is how it behaves with a disk
// attached.
//
// A disk that reproduces anything has to be partitioned and formatted: a raw unformatted
// image gets no volume and no drive letter, so the guest never sees a disk to prefer.
// The geometry here is therefore not arbitrary — one MBR partition starting at LBA 2048,
// formatted FAT — which is the shape measured to work against a Windows PE.
//
// Everything is done in-process through go-diskfs, so making a disk needs no sfdisk, no
// mkfs.vfat, and no root, and works the same in a container as on a workstation.
//
// The volume is exposed as an fs.FS (Image.FS), which is what source.Backing already is,
// so the inspection helpers that read a built image read a scratch disk unchanged.
package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/diskfs/go-diskfs"
	godisk "github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/fat12"
	"github.com/diskfs/go-diskfs/partition/mbr"
)

// The disk geometry every image this package writes uses.
//
// The partition starts a megabyte in (2048 × 512-byte sectors), which is both the
// conventional alignment and the offset the formatting recipe this package replaces was
// measured with. Sectors are 512 bytes because that is what the FAT implementations
// accept and what an XP-era guest expects.
const (
	sectorSize        = 512
	partitionStartLBA = 2048
	// alignmentGap is what the partition offset costs the volume: a caller asking for a
	// 256 MiB disk gets a volume a megabyte smaller, and Info reports the smaller number.
	alignmentGap = partitionStartLBA * sectorSize
)

// DefaultLabel is the volume label a created disk carries when none is given.
//
// A label is the only identity a volume has that survives into the guest, which is what
// makes "which volume is this?" answerable from inside a PE — the same mechanism the
// RAM disk's own label serves.
const DefaultLabel = "PE SCRATCH"

// maxLabel is the length of a FAT volume label, which is a fixed 11-byte field in the
// boot sector rather than a string with a limit.
const maxLabel = 11

// maxDisk is the largest disk an MBR can describe.
//
// A partition entry counts its start and length in 32-bit sectors, so 2^32 sectors of
// 512 bytes is the ceiling the table itself imposes. Past it the sector count wraps, and
// silently: a 3 TiB disk would take a partition length of about 1 TiB, which is small
// enough that the FAT32 formatter accepts it and Create returns success having built a
// volume nothing like the one asked for. It is refused here so that the failure is a
// message rather than a disk that lies about its size.
const maxDisk = int64(math.MaxUint32) * sectorSize

// The FAT16 volume bounds this package will format between.
//
// Both exist because a filesystem's real limits are its cluster count, not the round
// number its name suggests. fat16.Create enforces 4085 <= clusters < 65525 itself and
// reports either violation clearly, so neither bound is preventing a corrupt volume —
// they are here to refuse a size in our own words, and to keep FSAuto from routing a
// caller into a refusal it did not ask for.
//
// The maximum is the one that matters. fat12.Fat16MaxSize is a nominal 2 GiB, but at the
// 64 sectors/cluster chosen for volumes that large the cluster ceiling bites slightly
// below it — so a volume in the last megabyte of the nominal range is sent to FAT16 by
// its size and then refused by its cluster count. Backing off by a megabyte puts the
// whole FSAuto range inside what FAT16 can actually hold, and sends the rest to FAT32.
const (
	minFAT16Volume = 8 << 20
	maxFAT16Volume = fat12.Fat16MaxSize - (1 << 20)
)

// minFAT32Volume is the smallest volume this package will format FAT32.
//
// Unlike its FAT16 counterpart this one is load-bearing. fat32.Create checks only that
// the layout yields at least one cluster and 32 KiB of data area — it does not enforce
// the 65525 clusters below which a volume is not FAT32 by specification at all. At the
// 512-byte clusters it picks for small volumes that boundary is near 32.5 MiB, so a
// smaller disk formatted FAT32 gets an 0x0c partition type over a filesystem a guest is
// entitled to read as something else. Sixty-four megabytes clears it with margin, and is
// below nothing FSAuto would route here anyway: auto reaches FAT32 only past 2 GiB, so
// this only ever answers an explicit request for FAT32 on a disk too small to carry it.
const minFAT32Volume = 64 << 20

// FSType is the filesystem to put on a created disk.
type FSType int

const (
	// FSAuto picks from the volume size: FAT16 while it fits, FAT32 beyond.
	FSAuto FSType = iota
	// FAT16 is the filesystem an XP-era guest is most reliably at home with, and the one
	// the measured recipe used.
	FAT16
	// FAT32 is what a volume too large for FAT16 gets.
	FAT32
)

// String names the filesystem the way it is reported to a user.
func (t FSType) String() string {
	switch t {
	case FAT16:
		return "FAT16"
	case FAT32:
		return "FAT32"
	case FSAuto:
		return "auto"
	}
	return fmt.Sprintf("FSType(%d)", int(t))
}

// ParseFSType resolves the name a command-line flag carries.
func ParseFSType(s string) (FSType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return FSAuto, nil
	case "fat16":
		return FAT16, nil
	case "fat32":
		return FAT32, nil
	}
	return 0, fmt.Errorf("unknown filesystem %q; use fat16 or fat32", s)
}

// Spec describes a disk image to create.
type Spec struct {
	// Size is the whole-disk size in bytes. The volume is smaller by alignmentGap.
	Size int64
	// FS is the filesystem to format. The zero value chooses from Size.
	FS FSType
	// Label is the volume label. The zero value is DefaultLabel.
	Label string
}

// Info describes a disk image that exists.
type Info struct {
	// Path is the image file.
	Path string
	// DiskSize is the size of the image file.
	DiskSize int64
	// VolumeSize is the size of the FAT partition inside it, which is what a caller
	// should report: it is the space the guest actually sees, and it is smaller than the
	// size the disk was asked for.
	VolumeSize int64
	// FS is the filesystem found on the volume.
	FS FSType
	// Label is the volume label, trimmed.
	Label string
}

// Image is an open disk image.
type Image struct {
	disk *godisk.Disk
	fsys filesystem.FileSystem
	info Info
}

// FS returns the volume as a filesystem, which satisfies fs.FS (along with ReadDirFS,
// ReadFileFS and StatFS) and is therefore assignable to source.Backing. Reading a
// scratch disk needs no adapter and no second implementation of path matching.
//
// Name lookup is case-insensitive, as FAT itself is, which matches how a built image
// reads through its own backing.
func (i *Image) FS() filesystem.FileSystem { return i.fsys }

// Info describes the image, for a caller that has to report what it attached.
func (i *Image) Info() Info { return i.info }

// Close releases the image. Writes made through FS are flushed to the backing file as
// they are made, so a caller that has written must still Close before reading the file
// back through a fresh Open.
func (i *Image) Close() error {
	// The FAT implementations' Close is a no-op today, but it is part of the filesystem
	// contract and a future one may have cleanup to do; the backend close is the one that
	// must not be skipped, so its error is the one reported.
	if i.fsys != nil {
		_ = i.fsys.Close()
	}
	if i.disk != nil {
		return i.disk.Close()
	}
	return nil
}

// Create writes a new disk image: one MBR partition starting at LBA 2048, formatted FAT
// and labelled.
//
// It refuses to write over an existing file. A disk is a lasting artifact that may hold
// whatever a guest last wrote to it, so replacing one is never something this package
// does on a caller's behalf.
//
// A failure at any point removes the partial file: a half-written image that later gets
// attached to a boot is worse than no image at all.
func Create(path string, s Spec) (err error) {
	fsType, err := s.resolve()
	if err != nil {
		return err
	}
	label, err := normalizeLabel(s.Label)
	if err != nil {
		return err
	}

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; delete it or choose another path", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	defer func() {
		if err != nil {
			// Best effort: the error being returned is the one worth reporting, and a
			// removal that fails leaves a file the caller can see and delete.
			_ = os.Remove(path)
		}
	}()

	d, err := diskfs.Create(path, s.Size, diskfs.SectorSize512)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if cerr := d.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	table := &mbr.Table{
		LogicalSectorSize:  sectorSize,
		PhysicalSectorSize: sectorSize,
		Partitions: []*mbr.Partition{{
			// The index is load-bearing, not decoration: CreateFilesystem finds its
			// partition by matching this field, so a table built without it formats
			// nothing and fails with "requested partition 1 not found".
			Index: 1,
			Type:  partitionType(fsType),
			Start: partitionStartLBA,
			Size:  uint32(s.Size/sectorSize) - partitionStartLBA,
			// Deliberately not bootable: a scratch disk is data. The boot order stays the
			// launcher's to state, and nothing attached should look bootable to firmware.
			Bootable: false,
		}},
	}
	if err := d.Partition(table); err != nil {
		return fmt.Errorf("partition %s: %w", path, err)
	}

	if _, err := d.CreateFilesystem(godisk.FilesystemSpec{
		Partition:   1,
		FSType:      fsType.filesystemType(),
		VolumeLabel: label,
	}); err != nil {
		return fmt.Errorf("format %s as %s: %w", path, fsType, err)
	}
	return nil
}

// Open opens an existing disk image for reading and writing.
//
// It reads any raw image with an MBR and a FAT partition, not only the ones this package
// wrote — a disk prepared by another tool is exactly as useful to attach, and refusing it
// would make the reader useless for half the disks a caller has.
func Open(path string) (*Image, error) {
	format, err := SniffFormat(path)
	if err != nil {
		return nil, err
	}
	if format != FormatRaw {
		return nil, fmt.Errorf("%s is a %s image; only raw images can be read directly (convert it with: qemu-img convert -f %s -O raw %s out.img)",
			path, format, format, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	d, err := diskfs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = d.Close()
		}
	}()

	table, err := d.GetPartitionTable()
	if err != nil {
		return nil, fmt.Errorf("%s has no readable partition table, so it is not a formatted disk image: %w", path, err)
	}
	// An MBR always reports four entries, empty ones included, so an empty table is not
	// a short slice — it is four entries of type zero. Saying that plainly is the
	// difference between "this disk holds nothing" and a confusing complaint about a
	// filesystem in a partition that was never allocated.
	parts := table.GetPartitions()
	if len(parts) == 0 || partitionTypeByte(parts[0]) == 0 {
		return nil, fmt.Errorf("%s has a partition table but nothing in its first partition; this tool reads a disk with one FAT volume there", path)
	}

	fsys, err := d.GetFilesystem(1)
	if err != nil {
		// Naming the type byte turns "this did not work" into something actionable: it
		// says what the disk actually holds, which is usually the whole answer.
		return nil, fmt.Errorf("%s has no readable FAT volume in its first partition (type 0x%02x): %w",
			path, partitionTypeByte(parts[0]), err)
	}

	fsType, err := fromFilesystemType(fsys.Type())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	ok = true
	return &Image{
		disk: d,
		fsys: fsys,
		info: Info{
			Path:       path,
			DiskSize:   info.Size(),
			VolumeSize: parts[0].GetSize(),
			FS:         fsType,
			Label:      strings.TrimSpace(fsys.Label()),
		},
	}, nil
}

// resolve settles the filesystem for a spec and checks the size can carry it.
//
// The choice is made against the *volume* size rather than the disk size, because that is
// what gets formatted and what the filesystem's own limits are stated against. A disk
// asked for at exactly the FAT16 ceiling therefore still lands on FAT16, its volume being
// a megabyte smaller.
func (s Spec) resolve() (FSType, error) {
	if s.Size <= 0 {
		return 0, fmt.Errorf("a disk size is required")
	}
	if s.Size%sectorSize != 0 {
		return 0, fmt.Errorf("size %d is not a whole number of %d-byte sectors", s.Size, sectorSize)
	}
	// Checked before anything derives a sector count from the size: past this the count
	// no longer fits the 32 bits a partition entry gives it, and the conversion wraps
	// rather than failing — a 3 TiB disk would be partitioned as about 1 TiB.
	if s.Size > maxDisk {
		return 0, fmt.Errorf("size %d is larger than the %d bytes an MBR can describe; a disk this large needs a partitioning scheme this tool does not write",
			s.Size, maxDisk)
	}
	volume := s.Size - alignmentGap
	if volume <= 0 {
		return 0, fmt.Errorf("size %d leaves no room for a volume after the %d-byte partition alignment; use at least %d",
			s.Size, alignmentGap, alignmentGap+minFAT16Volume)
	}

	switch s.FS {
	case FSAuto:
		if volume < minFAT16Volume {
			return 0, fmt.Errorf("size %d leaves a %d-byte volume, too small to be a valid FAT16 filesystem; use at least %d",
				s.Size, volume, alignmentGap+minFAT16Volume)
		}
		// The comparison is against what FAT16 can really hold, not its nominal ceiling:
		// choosing it by the nominal one would route the last megabyte of the range into
		// a cluster-count refusal, which is a failure the caller never asked for when
		// they left the filesystem to us.
		if volume <= maxFAT16Volume {
			return FAT16, nil
		}
		return FAT32, nil
	case FAT16:
		if volume > maxFAT16Volume {
			return 0, fmt.Errorf("size %d leaves a %d-byte volume, larger than the %d bytes FAT16 can address; use fat32 or a smaller size",
				s.Size, volume, maxFAT16Volume)
		}
		if volume < minFAT16Volume {
			return 0, fmt.Errorf("size %d leaves a %d-byte volume, too small to be a valid FAT16 filesystem; use at least %d",
				s.Size, volume, alignmentGap+minFAT16Volume)
		}
		return FAT16, nil
	case FAT32:
		if volume < minFAT32Volume {
			return 0, fmt.Errorf("size %d leaves a %d-byte volume, too small to be a valid FAT32 filesystem; use at least %d, or leave the filesystem unset to get FAT16",
				s.Size, volume, alignmentGap+minFAT32Volume)
		}
		return FAT32, nil
	}
	return 0, fmt.Errorf("unknown filesystem %d", int(s.FS))
}

// partitionType is the MBR type byte for a filesystem.
//
// The FAT16 byte is 0x06 (mbr.Fat16b), not the 0x04 that mbr.Fat16 names: 0x04 is the
// pre-LBA type for volumes under 32 MB, and 0x06 is what a modern formatter writes and
// what the recipe this package replaces was measured with. The tempting constant is the
// wrong one.
func partitionType(t FSType) mbr.Type {
	if t == FAT32 {
		return mbr.Fat32LBA
	}
	return mbr.Fat16b
}

// filesystemType maps to the library's own filesystem enum.
func (t FSType) filesystemType() filesystem.Type {
	if t == FAT32 {
		return filesystem.TypeFat32
	}
	return filesystem.TypeFat16
}

// fromFilesystemType maps the library's enum back, for a volume this package is reading
// rather than writing. FAT12 is reported as FAT16: the two are one implementation and one
// family, and the distinction is not one a caller attaching a disk can act on.
func fromFilesystemType(t filesystem.Type) (FSType, error) {
	switch t {
	case filesystem.TypeFat12, filesystem.TypeFat16:
		return FAT16, nil
	case filesystem.TypeFat32:
		return FAT32, nil
	}
	return 0, fmt.Errorf("the volume is not FAT")
}

// partitionTypeByte reads the MBR type byte back off a partition, for an error message
// that can say what a disk actually holds. A partition from any other table shape has no
// such byte, and reports zero.
func partitionTypeByte(p interface{ GetStart() int64 }) byte {
	if mp, ok := p.(*mbr.Partition); ok {
		return byte(mp.Type)
	}
	return 0
}

// labelForbidden are the characters a FAT volume label may not contain. The label shares
// the short-name character rules, so the set is the one that would otherwise produce a
// name the guest reads differently from the one that was asked for.
const labelForbidden = `*?.,;:/\|+=<>[]"`

// normalizeLabel returns the label to write, or an error naming what is wrong with it.
//
// The label is uppercased, which is how FAT stores one — a lowercase label is not an
// error, it simply is not a thing the field can hold. Length and character violations are
// refused rather than trimmed away: silently shortening a label produces a volume whose
// identity is not the one the caller asked for, which is precisely what a label is for.
func normalizeLabel(label string) (string, error) {
	if label == "" {
		label = DefaultLabel
	}
	// Characters are checked before length, and against what the caller wrote rather than
	// the uppercased form. Uppercasing can lengthen a string — ß becomes SS — so a length
	// measured after it would refuse a label the caller can see is short, quoting a count
	// belonging to neither what they typed nor what it holds. Rejecting the non-ASCII
	// character first means that case never reaches the length check at all.
	for _, r := range label {
		if r < 0x20 || r > 0x7e {
			return "", fmt.Errorf("volume label %q contains a character a FAT label cannot hold", label)
		}
		if strings.ContainsRune(labelForbidden, r) {
			return "", fmt.Errorf("volume label %q contains %q, which a FAT label cannot hold", label, r)
		}
	}
	// Every character is now known to be one ASCII byte, so a rune count and a byte count
	// agree; counting runes is what makes that true rather than assumed.
	if n := utf8.RuneCountInString(label); n > maxLabel {
		return "", fmt.Errorf("volume label %q is %d characters; a FAT label holds at most %d",
			label, n, maxLabel)
	}
	return strings.ToUpper(label), nil
}
