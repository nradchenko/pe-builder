// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package boot handles the boot-file layout for a bootable image: extracting the
// El Torito boot image from the source ISO and placing the built-in startup
// script. The boot loader, hardware detector, and boot-prompt binaries themselves
// come from the source through the base file selection; this package supplies the
// pieces that are not plain source-file copies.
package boot

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// El Torito on-disk constants (see ECMA-119 / the "El Torito" bootable-CD spec).
const (
	sectorSize      = 2048 // ISO9660 logical sector
	bootRecordLBA   = 0x11 // the boot-record volume descriptor sits at sector 17
	catalogPtrOff   = 0x47 // offset of the boot-catalog pointer within the boot record
	defaultEntryOff = 0x20 // the initial/default entry follows the 0x20-byte validation entry
	virtualSector   = 512  // El Torito counts an image in 512-byte virtual sectors

	entryBootable   = 0x88 // boot-indicator of a bootable entry
	emulNoEmulation = 0x00 // "no emulation" media type
)

// ExtractBootImage reads the El Torito boot image from the raw ISO bytes in r —
// the Microsoft CDBOOT sector an XP/2003 CD boots from, which itself loads
// \I386\SETUPLDR.BIN. Mastering our own ISO reuses this exact image, so the boot
// chain is the source's own, unmodified. The caller (which owns knowing the source
// is an ISO) supplies the reader, e.g. from source.OpenImageFile.
//
// It parses the boot-record volume descriptor, follows it to the boot catalog,
// and returns the bytes of the default (initial) entry's image. It requires a
// no-emulation, bootable entry — what XP install media carry — and errors on
// anything else.
func ExtractBootImage(r io.ReaderAt) ([]byte, error) {
	// Boot-record volume descriptor: type 0, standard identifier "CD001".
	rec := make([]byte, sectorSize)
	if _, err := r.ReadAt(rec, bootRecordLBA*sectorSize); err != nil {
		return nil, fmt.Errorf("boot: read boot record: %w", err)
	}
	if rec[0] != 0x00 || string(rec[1:6]) != "CD001" {
		return nil, fmt.Errorf("boot: source is not an El Torito bootable ISO (no boot record at sector %d)", bootRecordLBA)
	}

	catalogLBA := binary.LittleEndian.Uint32(rec[catalogPtrOff : catalogPtrOff+4])
	cat := make([]byte, sectorSize)
	if _, err := r.ReadAt(cat, int64(catalogLBA)*sectorSize); err != nil {
		return nil, fmt.Errorf("boot: read boot catalog: %w", err)
	}

	entry := cat[defaultEntryOff : defaultEntryOff+0x20]
	if entry[0] != entryBootable {
		return nil, fmt.Errorf("boot: default El Torito entry is not bootable (0x%02x)", entry[0])
	}
	if entry[1] != emulNoEmulation {
		return nil, fmt.Errorf("boot: default El Torito entry is not no-emulation (0x%02x)", entry[1])
	}

	sectors := binary.LittleEndian.Uint16(entry[6:8])
	imageLBA := binary.LittleEndian.Uint32(entry[8:12])
	if sectors == 0 {
		return nil, fmt.Errorf("boot: El Torito entry has a zero-length image")
	}

	img := make([]byte, int(sectors)*virtualSector)
	if _, err := r.ReadAt(img, int64(imageLBA)*sectorSize); err != nil {
		return nil, fmt.Errorf("boot: read boot image: %w", err)
	}
	return img, nil
}

// WriteBootImage extracts the source ISO's El Torito boot image from r and writes
// it to <outDir>/bootsect.bin, the boot image the ISO master hands to El Torito. It
// returns the number of bytes written.
func WriteBootImage(r io.ReaderAt, outDir string) (int, error) {
	img, err := ExtractBootImage(r)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(bootSectPath(outDir), img, 0o644); err != nil {
		return 0, fmt.Errorf("boot: write bootsect.bin: %w", err)
	}
	return len(img), nil
}
