// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0
//
// MSCF cabinet container parsing: the CFHEADER/CFFOLDER/CFFILE/CFDATA framing
// that wraps a folder's compressed data blocks. The on-disk layout follows
// Microsoft's published cabinet (.cab) file format. Only single-cabinet
// archives are handled; folders and files that continue across cabinet
// boundaries are out of scope.

package compress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Cabinet header flags.
const (
	cfhdrPrevCabinet    = 0x0001
	cfhdrNextCabinet    = 0x0002
	cfhdrReservePresent = 0x0004
)

// Folder compression methods (low byte of CFFOLDER.typeCompress).
const (
	compMask    = 0x000f
	compNone    = 0
	compMSZIP   = 1
	compQuantum = 2
	compLZX     = 3
)

var cabMagic = []byte{'M', 'S', 'C', 'F'}

// CabinetFile describes one member file of a cabinet.
type CabinetFile struct {
	Name   string
	Size   uint32
	folder int
	offset uint32 // byte offset within the folder's uncompressed stream
}

// Cabinet is a parsed MSCF cabinet ready for per-file extraction. It is safe for
// concurrent use: methods may be called from multiple goroutines.
type Cabinet struct {
	data        []byte
	dataReserve int // per-CFDATA reserved bytes (cbCFData)
	folders     []cabFolder
	files       []CabinetFile

	mu      sync.Mutex     // guards decoded
	decoded map[int][]byte // folder index -> decompressed bytes (lazy)
}

type cabFolder struct {
	coffCabStart uint32
	blocks       uint16
	typeCompress uint16
}

// IsCabinet reports whether b begins with the MSCF cabinet signature.
func IsCabinet(b []byte) bool {
	return bytes.HasPrefix(b, cabMagic)
}

// ReadCabinet parses the framing of an MSCF cabinet. The backing slice is
// retained and read from lazily during extraction, so it must not be mutated.
func ReadCabinet(data []byte) (*Cabinet, error) {
	if !IsCabinet(data) {
		return nil, errors.New("not an MSCF cabinet")
	}
	r := &cursor{b: data}
	r.skip(4)            // signature (validated above)
	r.skip(4)            // reserved1
	r.u32()              // cbCabinet (total size)
	r.skip(4)            // reserved2
	coffFiles := r.u32() // offset of the first CFFILE
	r.skip(4)            // reserved3
	r.skip(2)            // versionMinor, versionMajor
	cFolders := r.u16()  //
	cFiles := r.u16()    //
	flags := r.u16()     //
	r.skip(2)            // setID
	r.skip(2)            // iCabinet
	if r.err != nil {
		return nil, fmt.Errorf("cabinet header: %w", r.err)
	}

	folderReserve := 0
	dataReserve := 0
	if flags&cfhdrReservePresent != 0 {
		cbCFHeader := int(r.u16())
		folderReserve = int(r.u8())
		dataReserve = int(r.u8())
		r.skip(cbCFHeader) // abReserve
	}
	// Optional previous/next cabinet name pairs (multi-cabinet chains).
	if flags&cfhdrPrevCabinet != 0 {
		r.asciiz()
		r.asciiz()
	}
	if flags&cfhdrNextCabinet != 0 {
		r.asciiz()
		r.asciiz()
	}
	if r.err != nil {
		return nil, fmt.Errorf("cabinet header reserve: %w", r.err)
	}

	c := &Cabinet{data: data, dataReserve: dataReserve, decoded: map[int][]byte{}}

	c.folders = make([]cabFolder, cFolders)
	for i := range c.folders {
		c.folders[i] = cabFolder{
			coffCabStart: r.u32(),
			blocks:       r.u16(),
			typeCompress: r.u16(),
		}
		r.skip(folderReserve) // abReserve
	}
	if r.err != nil {
		return nil, fmt.Errorf("cabinet folders: %w", r.err)
	}

	r.pos = int(coffFiles)
	c.files = make([]CabinetFile, 0, cFiles)
	for i := 0; i < int(cFiles); i++ {
		size := r.u32()
		offset := r.u32()
		folder := r.u16()
		r.skip(6) // date, time, attribs
		name := r.asciiz()
		if r.err != nil {
			return nil, fmt.Errorf("cabinet file %d: %w", i, r.err)
		}
		if int(folder) >= len(c.folders) {
			return nil, fmt.Errorf("cabinet file %q references folder %d of %d",
				name, folder, len(c.folders))
		}
		c.files = append(c.files, CabinetFile{
			Name:   name,
			Size:   size,
			folder: int(folder),
			offset: offset,
		})
	}
	return c, nil
}

// Files returns the cabinet's member files in archive order.
func (c *Cabinet) Files() []CabinetFile { return c.files }

// Find returns the file with the given name (case-insensitive).
func (c *Cabinet) Find(name string) (CabinetFile, bool) {
	for _, f := range c.files {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return CabinetFile{}, false
}

// Data decompresses (if necessary) the folder holding f and returns f's bytes.
// A folder is decoded once and cached, since extracting any file requires
// decoding the folder from its start.
func (c *Cabinet) Data(f CabinetFile) ([]byte, error) {
	folder, err := c.folder(f.folder)
	if err != nil {
		return nil, err
	}
	// Bounds are computed in int64 so a hostile offset/size can't overflow int
	// on 32-bit builds and slip past the check.
	start := int64(f.offset)
	end := start + int64(f.Size)
	if start > int64(len(folder)) || end > int64(len(folder)) {
		return nil, fmt.Errorf("file %q extends past its folder (%d+%d > %d)",
			f.Name, f.offset, f.Size, len(folder))
	}
	// Three-index slice: the returned bytes share the cached folder buffer, so
	// capping capacity stops a caller's append from overwriting sibling files.
	return folder[start:end:end], nil
}

// folder returns the decompressed bytes of folder idx, decoding and caching on
// first use. The lock is held only around the cache map; two goroutines racing
// the same fresh folder may both decode it (harmless, last write wins), while
// different folders decode concurrently.
func (c *Cabinet) folder(idx int) ([]byte, error) {
	c.mu.Lock()
	b, ok := c.decoded[idx]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := c.decodeFolder(idx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.decoded[idx] = b
	c.mu.Unlock()
	return b, nil
}

// decodeFolder concatenates a folder's CFDATA payloads and runs them through the
// folder's compression method.
func (c *Cabinet) decodeFolder(idx int) ([]byte, error) {
	fld := c.folders[idx]

	// First pass: validate the block headers and total the compressed and
	// uncompressed sizes, so the payload buffer is allocated once. Each CFDATA
	// block holds at most one 32 KiB frame; rejecting a larger declared size
	// both matches the format and stops a tiny cabinet from steering the
	// uncompressed total (and its allocation) to an absurd value.
	r := &cursor{b: c.data, pos: int(fld.coffCabStart)}
	compLen, uncompLen := 0, 0
	for i := 0; i < int(fld.blocks); i++ {
		r.u32()                  // csum
		cbData := int(r.u16())   // compressed byte count
		cbUncomp := int(r.u16()) // uncompressed byte count
		r.skip(c.dataReserve)    // abReserve
		r.skip(cbData)           // ab (compressed data)
		if r.err != nil {
			return nil, fmt.Errorf("folder %d block %d: %w", idx, i, r.err)
		}
		if cbUncomp > lzxFrameSize {
			return nil, fmt.Errorf("folder %d block %d declares oversized uncompressed size %d",
				idx, i, cbUncomp)
		}
		compLen += cbData
		uncompLen += cbUncomp
	}

	// Second pass: copy the CFDATA payloads into one preallocated buffer.
	r = &cursor{b: c.data, pos: int(fld.coffCabStart)}
	comp := make([]byte, 0, compLen)
	for i := 0; i < int(fld.blocks); i++ {
		r.u32()
		cbData := int(r.u16())
		r.u16()
		r.skip(c.dataReserve)
		comp = append(comp, r.take(cbData)...)
	}
	if r.err != nil {
		return nil, fmt.Errorf("folder %d: %w", idx, r.err)
	}

	switch method := fld.typeCompress & compMask; method {
	case compNone:
		return comp, nil
	case compLZX:
		windowBits := int((fld.typeCompress >> 8) & 0x1f)
		return lzxDecode(comp, windowBits, uncompLen)
	case compMSZIP:
		return nil, errors.New("MSZIP cabinet compression is not yet supported")
	case compQuantum:
		return nil, errors.New("unsupported Quantum cabinet compression")
	default:
		return nil, fmt.Errorf("unknown cabinet compression method %d", method)
	}
}

// --- byte cursor ------------------------------------------------------------

// cursor is a little-endian byte reader that latches the first out-of-bounds
// read into err, so callers can parse a run of fields and check once.
type cursor struct {
	b   []byte
	pos int
	err error
}

func (c *cursor) remaining() int { return len(c.b) - c.pos }

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	// c.pos < 0 guards against a header offset (a uint32 seek target) that
	// overflowed to a negative int on a 32-bit build, which would otherwise
	// fool the remaining() check and panic the slice.
	if n < 0 || c.pos < 0 || c.remaining() < n {
		c.err = errors.New("unexpected end of cabinet data")
		return nil
	}
	b := c.b[c.pos : c.pos+n]
	c.pos += n
	return b
}

func (c *cursor) skip(n int) { c.take(n) }

func (c *cursor) u8() uint8 {
	b := c.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (c *cursor) u16() uint16 {
	b := c.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (c *cursor) u32() uint32 {
	b := c.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

// asciiz reads a NUL-terminated string.
func (c *cursor) asciiz() string {
	if c.err != nil {
		return ""
	}
	start := c.pos
	for c.pos < len(c.b) {
		if c.b[c.pos] == 0 {
			s := string(c.b[start:c.pos])
			c.pos++ // consume the terminator
			return s
		}
		c.pos++
	}
	c.err = errors.New("unterminated cabinet string")
	return ""
}
