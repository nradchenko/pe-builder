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
	// lf is the fast leaf: the subkey index every 1.3 hive Microsoft ships uses (the
	// setupreg.hiv of Windows 2000, XP and Server 2003 alike), and the only leaf form
	// Windows 2000 reads. The hash leaf (lh) that XP introduced is read by XP and later
	// but rejected outright by 2000's loader — "\WINNT\SYSTEM32\CONFIG\SYSTEM is missing
	// or corrupt" — so this writer emits lf, which every NT reads and which is what the
	// minor version it declares implies.
	sigLF = []byte("lf")
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
	binary.LittleEndian.PutUint32(b[0x18:], 3) // minor version: what every NT 5.x writes
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

// nameHint computes a fast-leaf (lf) element's name hint: the first four characters
// of the key name as stored, one byte each, zero-padded when the name is shorter. The
// kernel uses it to skip subkeys whose hint cannot match before comparing full names,
// so a wrong byte hides a subkey; a zero byte is "no hint" and always matches. A
// character above 0xFF has no byte form, and it and everything after it are left
// zero, which is what Windows writes for such names. Case is kept as stored — the
// kernel's hint comparison is case-insensitive, and Microsoft's own hives carry the
// name's case in the hint ("Allo" for Allocation, "{4D3" for a class GUID).
func nameHint(nameBytes []byte, compressed bool) [4]byte {
	var hint [4]byte
	if compressed {
		copy(hint[:], nameBytes)
		return hint
	}
	for i := 0; i < 4 && 2*i+1 < len(nameBytes); i++ {
		c := binary.LittleEndian.Uint16(nameBytes[2*i:])
		if c > 0xFF {
			break
		}
		hint[i] = byte(c)
	}
	return hint
}
