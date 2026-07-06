// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import "encoding/binary"

// regf on-disk constants.
const (
	baseBlockSize = 4096 // the file header block precedes all hive bins
	hbinSize      = 4096 // hive-bin granularity; bins are a multiple of this
	hbinHeaderLen = 0x20 // bytes of hbin header before the first cell
	cellAlign     = 8    // cell sizes are rounded up to this

	// none marks an absent cell reference (subkey list, value list, data, class).
	none = uint32(0xFFFFFFFF)

	// maxCellData is the largest value payload we place in a single data cell;
	// beyond this the format requires a big-data (db) record, which is guarded.
	maxCellData = 16344
	// maxSubkeys is the most subkeys one leaf-index cell can list (the count is a
	// uint16); beyond this the format requires a root index (ri), which is guarded.
	maxSubkeys = 65535

	// fixedFiletime is the deterministic write timestamp stamped into every hive
	// and node, so output is byte-reproducible (100ns ticks since 1601; 2020-01-01).
	fixedFiletime = uint64(132223104000000000)
)

// Cell/record signatures (their little-endian 16- or 32-bit on-disk form is
// produced by copying these bytes at the start of the record).
var (
	sigRegf = []byte("regf")
	sigHbin = []byte("hbin")
	sigNK   = []byte("nk")
	sigVK   = []byte("vk")
	sigSK   = []byte("sk")
	sigLH   = []byte("lh")
)

// buildBaseBlock returns the 4096-byte file header for a clean primary hive whose
// root cell is at rootOffset (relative to the start of hive-bins data) and whose
// hive bins total binsLen bytes.
func buildBaseBlock(rootOffset, binsLen uint32) []byte {
	b := make([]byte, baseBlockSize)
	copy(b[0:], sigRegf)
	binary.LittleEndian.PutUint32(b[0x04:], 1) // primary sequence
	binary.LittleEndian.PutUint32(b[0x08:], 1) // secondary sequence (== primary: clean)
	binary.LittleEndian.PutUint64(b[0x0C:], fixedFiletime)
	binary.LittleEndian.PutUint32(b[0x14:], 1) // major version
	binary.LittleEndian.PutUint32(b[0x18:], 3) // minor version (XP)
	binary.LittleEndian.PutUint32(b[0x1C:], 0) // file type: primary
	binary.LittleEndian.PutUint32(b[0x20:], 1) // file format: direct memory load
	binary.LittleEndian.PutUint32(b[0x24:], rootOffset)
	binary.LittleEndian.PutUint32(b[0x28:], binsLen)
	binary.LittleEndian.PutUint32(b[0x2C:], 1) // clustering factor
	// The embedded file-name field (0x30) and the recovery GUIDs are left zero.
	binary.LittleEndian.PutUint32(b[0x1FC:], headerChecksum(b))
	return b
}

// headerChecksum is the XOR of the first 127 little-endian dwords of the base
// block (offsets 0x000..0x1FB), with the two degenerate results remapped as the
// format requires.
func headerChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < 0x1FC; i += 4 {
		sum ^= binary.LittleEndian.Uint32(b[i:])
	}
	switch sum {
	case 0:
		return 1
	case 0xFFFFFFFF:
		return 0xFFFFFFFE
	default:
		return sum
	}
}

// nameHash computes the leaf-index (lh) name hash: for each character of the
// upper-cased name, hash = 37*hash + char. The kernel recomputes this on lookup,
// so it must match exactly for case-insensitive subkey lookups to succeed.
func nameHash(name string) uint32 {
	var h uint32
	for _, r := range upperASCII(name) {
		h = h*37 + uint32(r)
	}
	return h
}
