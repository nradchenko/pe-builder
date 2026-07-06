// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0
//
// This file implements a decoder for the cabinet (CAB) variant of the LZX
// compression algorithm.
//
// The canonical-Huffman table builder, the symbol decoder, the pre-tree delta
// reader, and the bit reader are reimplemented from the WIM-LZX decoder in
// github.com/Microsoft/go-winio (wim/lzx/lzx.go), which is Copyright (c) 2015
// Microsoft and licensed under the MIT License. That decoder targets the WIM
// variant of LZX; the CAB-specific framing here — the 24-bit block header, the
// Intel E8 call-translation header and per-frame fixup, up-to-21-bit window
// offsets, and the continuous cross-block bitstream — follows the CAB LZX
// behaviour of libmspack (mspack/lzxd.c, Copyright 2003-2004 Stuart Caie, LGPL),
// used purely as a format reference. No code from either project is copied
// verbatim; both are credited here to keep the provenance of this
// clean-room implementation unambiguous.

package compress

import (
	"encoding/binary"
	"errors"
	"io"
)

// LZX tuning constants shared by every window size.
const (
	lzxMinMatch          = 2   // shortest match length
	lzxNumChars          = 256 // literal alphabet size
	lzxNumPrimaryLengths = 7   // match lengths encoded directly in the main symbol
	lzxFrameSize         = 32768

	lzxPretreeElements = 20  // pre-tree used to encode the other trees
	lzxAlignedElements = 8   // aligned-offset tree
	lzxLenTableSize    = 249 // secondary length tree

	// The main tree splits into a fixed literal half and a window-dependent
	// match half of numOffsets symbols; the widest supported 21-bit window has
	// 50 position slots, i.e. 50*8 match symbols.
	maincodesplit   = lzxNumChars
	maxMainElements = lzxNumChars + 50*8
)

// Huffman decode-table parameters. A decode-table entry packs the symbol in the
// low lenshift bits and its code length above. The CAB main tree has up to 656
// symbols (256 literals + 50*8 match codes for a 21-bit window), so the symbol
// field must be 10 bits wide — unlike the WIM variant, whose 496-symbol
// alphabet fits in 9. tablebits (the first-level lookup width) is independent
// and stays at 9.
const (
	maxTreePathLen = 16 // longest permitted Huffman code
	lenshift       = 10 // symbol occupies bits [0,lenshift); code length above
	codemask       = 1<<lenshift - 1
	tablebits      = 9 // bits consumed by the first-level lookup table
	tablesize      = 1 << tablebits
)

// LZX block types.
const (
	lzxBlockVerbatim = 1
	lzxBlockAligned  = 2
	lzxBlockUncompr  = 3
)

// e8FrameLimit is the number of leading 32 KiB frames eligible for Intel E8
// translation (frames beyond ~1 GiB of output are never translated).
const e8FrameLimit = 32768

var errCorrupt = errors.New("LZX data corrupt")

// positionSlots maps (windowBits-15) to the number of LZX position slots.
var positionSlots = [...]int{30, 32, 34, 36, 38, 42, 50}

// lzxExtraBits[i] and lzxPositionBase[i] give the number of verbatim offset bits
// and the base offset for position slot i, derived from the standard LZX
// formulas rather than hard-coded.
var (
	lzxExtraBits    [51]byte
	lzxPositionBase [51]uint32
)

func init() {
	for i := range lzxExtraBits {
		switch {
		case i < 4:
			lzxExtraBits[i] = 0
		case i < 36:
			lzxExtraBits[i] = byte(i/2 - 1)
		default:
			lzxExtraBits[i] = 17
		}
	}
	var base uint32
	for i := range lzxPositionBase {
		lzxPositionBase[i] = base
		base += 1 << lzxExtraBits[i]
	}
}

// lzxDecode decompresses a complete CAB LZX stream. input is the concatenation
// of a folder's CFDATA payloads, windowBits is the folder's window size
// (15..21), and outputLen is the folder's total uncompressed length.
func lzxDecode(input []byte, windowBits, outputLen int) ([]byte, error) {
	if windowBits < 15 || windowBits > 21 {
		return nil, errors.New("unsupported LZX window size")
	}
	if outputLen < 0 {
		return nil, errors.New("invalid LZX output length")
	}
	d := &lzxDecompressor{
		in:         input,
		numOffsets: positionSlots[windowBits-15] << 3,
		R0:         1,
		R1:         1,
		R2:         1,
	}
	d.mainElems = lzxNumChars + d.numOffsets
	return d.decode(outputLen)
}

type lzxDecompressor struct {
	// Bit reader over the compressed input. Bits are drawn from 16-bit
	// little-endian words, most-significant bit first. c holds the pending bits
	// left-aligned; nbits counts how many of its high bits are valid.
	in    []byte
	inPos int
	c     uint32
	nbits byte
	eof   bool
	err   error // sticky: set when the input is exhausted mid-decode

	// LZX state, persisted across blocks within the stream.
	numOffsets  int
	mainElems   int
	R0, R1, R2  uint32
	blockType   byte
	blockLeft   uint32 // bytes remaining in the current block
	curBlockLen uint32 // length of the current block (for the odd-length pad check)
	unaligned   bool   // previous uncompressed block had an odd length

	mainlens [maxMainElements]byte
	lenlens  [lzxLenTableSize]byte

	hmain, hlen, halign *huffman

	// Intel E8 call-translation header state.
	headerRead   bool
	intelSize    int32
	intelStarted bool
	intelStart   int // output offset where E8 translation first becomes live

	// Decompressed history. Because the whole folder is buffered, matches can
	// reference anywhere within the output produced so far, so no separate
	// circular window is needed.
	out []byte
}

// --- bit reader -------------------------------------------------------------

// haveInput reports whether n more input bytes are available.
func (d *lzxDecompressor) haveInput(n int) bool { return d.inPos+n <= len(d.in) }

// feed pulls another 16-bit little-endian word into the bit buffer. It returns
// false when the input is exhausted.
func (d *lzxDecompressor) feed() bool {
	if !d.haveInput(2) {
		d.eof = true
		return false
	}
	w := uint32(d.in[d.inPos]) | uint32(d.in[d.inPos+1])<<8
	d.c |= w << (16 - d.nbits)
	d.nbits += 16
	d.inPos += 2
	return true
}

// getBits reads n bits (0 <= n <= 16) from the stream.
func (d *lzxDecompressor) getBits(n byte) uint16 {
	if n == 0 {
		return 0
	}
	if d.nbits < n {
		// getBits always consumes the n bits it reads, so a short feed here is a
		// genuine truncation (unlike getCode's lookahead feed). Flag it and stop
		// rather than letting the byte-width nbits underflow, which would blind
		// every later EOF check.
		d.feed()
		if d.nbits < n {
			if d.err == nil {
				d.err = io.ErrUnexpectedEOF
			}
			return 0
		}
	}
	v := uint16(d.c >> (32 - n))
	d.c <<= n
	d.nbits -= n
	return v
}

// getBitsLong reads up to 17 bits, which a verbatim block's largest offset slot
// requires; getBits alone is limited to 16.
func (d *lzxDecompressor) getBitsLong(n byte) uint32 {
	if n <= 16 {
		return uint32(d.getBits(n))
	}
	hi := uint32(d.getBits(n - 16))
	lo := uint32(d.getBits(16))
	return hi<<16 | lo
}

// getCode decodes a single Huffman symbol using the given table.
func (d *lzxDecompressor) getCode(h *huffman) (uint16, error) {
	if h.maxbits == 0 {
		return 0, errCorrupt
	}
	if d.nbits < maxTreePathLen {
		d.feed()
	}
	// Codes shorter than tablebits resolve in the first-level table; longer
	// codes index a second-level table keyed by the remaining bits.
	c := h.table[d.c>>(32-tablebits)]
	if c < 1<<lenshift {
		c = h.extra[c][d.c<<tablebits>>(32-(h.maxbits-tablebits))]
	}
	n := byte(c >> lenshift)
	if d.nbits < n {
		return 0, io.ErrUnexpectedEOF
	}
	d.c <<= n
	d.nbits -= n
	return c & codemask, nil
}

// align16 discards the fractional remainder of the current 16-bit word so the
// underlying byte stream is 16-bit aligned, matching LZX uncompressed-block
// framing. When already aligned it skips a whole padding word.
func (d *lzxDecompressor) align16() {
	if d.nbits == 0 {
		d.feed()
	}
	d.getBits(d.nbits)
}

// alignFrame realigns the input to the next 16-bit boundary. The encoder
// flushes its bit buffer to a 16-bit boundary at the end of each 32 KiB output
// frame; dropping the fractional bits (the zero padding) resynchronises the
// reader with the next frame's byte-aligned data. Because feed only ever loads
// whole 16-bit words, the buffered bit count modulo 16 equals that padding.
func (d *lzxDecompressor) alignFrame() {
	drop := d.nbits % 16
	d.c <<= drop
	d.nbits -= drop
}

// readBytes copies len(dst) raw bytes directly from the input, bypassing the bit
// buffer (which must be 16-bit aligned and empty). Used for uncompressed blocks
// and their LRU header.
func (d *lzxDecompressor) readBytes(dst []byte) error {
	if !d.haveInput(len(dst)) {
		return io.ErrUnexpectedEOF
	}
	copy(dst, d.in[d.inPos:d.inPos+len(dst)])
	d.inPos += len(dst)
	return nil
}

// --- Huffman tables ---------------------------------------------------------

type huffman struct {
	extra   [][]uint16
	maxbits byte
	table   [tablesize]uint16
}

// buildTable builds a canonical Huffman decode table from per-symbol code
// lengths. It returns nil if the lengths do not form a complete code, or an
// empty table (maxbits == 0) if no symbol has a length.
func buildTable(codelens []byte) *huffman {
	var count [maxTreePathLen + 1]uint
	var maxLen byte
	for _, cl := range codelens {
		count[cl]++
		if maxLen < cl {
			maxLen = cl
		}
	}
	if maxLen == 0 {
		return &huffman{}
	}

	// First code value of each length, per the canonical construction.
	var first [maxTreePathLen + 1]uint
	code := uint(0)
	for i := byte(1); i <= maxLen; i++ {
		code <<= 1
		first[i] = code
		code += count[i]
	}
	if code != 1<<maxLen {
		return nil
	}

	h := &huffman{maxbits: maxLen}
	if maxLen > tablebits {
		core := first[tablebits+1] / 2 // codes that fit without a second level
		nextra := 1<<tablebits - core
		h.extra = make([][]uint16, nextra)
		for c := core; c < 1<<tablebits; c++ {
			h.table[c] = uint16(c - core)
			h.extra[c-core] = make([]uint16, 1<<(maxLen-tablebits))
		}
	}

	for i, cl := range codelens {
		if cl == 0 {
			continue
		}
		c := first[cl]
		first[cl]++
		v := uint16(cl)<<lenshift | uint16(i)
		if cl <= tablebits {
			extended := c << (tablebits - cl)
			for j := uint(0); j < 1<<(tablebits-cl); j++ {
				h.table[extended+j] = v
			}
		} else {
			prefix := c >> (cl - tablebits)
			suffix := c & (1<<(cl-tablebits) - 1)
			extended := suffix << (maxLen - cl)
			for j := uint(0); j < 1<<(maxLen-cl); j++ {
				h.extra[h.table[prefix]][extended+j] = v
			}
		}
	}
	return h
}

// readLengths updates a tree's code lengths in place by decoding the delta
// encoding carried by a pre-tree. lens holds the previous block's lengths (zero
// for the first block).
func (d *lzxDecompressor) readLengths(lens []byte) error {
	var pretreeLen [lzxPretreeElements]byte
	for i := range pretreeLen {
		pretreeLen[i] = byte(d.getBits(4))
	}
	h := buildTable(pretreeLen[:])
	if h == nil {
		return errCorrupt
	}

	for i := 0; i < len(lens); {
		c, err := d.getCode(h)
		if err != nil {
			return err
		}
		switch {
		case c <= 16: // length is a delta from the previous length
			lens[i] = (lens[i] + 17 - byte(c)) % 17
			i++
		case c == 17: // run of getBits(4)+4 zero lengths
			n := int(d.getBits(4)) + 4
			if i+n > len(lens) {
				return errCorrupt
			}
			for j := 0; j < n; j++ {
				lens[i+j] = 0
			}
			i += n
		case c == 18: // run of getBits(5)+20 zero lengths
			n := int(d.getBits(5)) + 20
			if i+n > len(lens) {
				return errCorrupt
			}
			for j := 0; j < n; j++ {
				lens[i+j] = 0
			}
			i += n
		case c == 19: // run of getBits(1)+4 identical lengths
			n := int(d.getBits(1)) + 4
			if i+n > len(lens) {
				return errCorrupt
			}
			cc, err := d.getCode(h)
			if err != nil {
				return err
			}
			if cc > 16 {
				return errCorrupt
			}
			l := (lens[i] + 17 - byte(cc)) % 17
			for j := 0; j < n; j++ {
				lens[i+j] = l
			}
			i += n
		default:
			return errCorrupt
		}
	}
	return nil
}

// readTrees reads the main and length trees, plus the aligned-offset tree for
// aligned-offset blocks.
func (d *lzxDecompressor) readTrees(aligned bool) error {
	if aligned {
		var alignedLen [lzxAlignedElements]byte
		for i := range alignedLen {
			alignedLen[i] = byte(d.getBits(3))
		}
		d.halign = buildTable(alignedLen[:])
		if d.halign == nil {
			return errCorrupt
		}
	} else {
		d.halign = nil
	}

	// The main tree is transmitted as two separately pre-tree-coded halves.
	if err := d.readLengths(d.mainlens[:maincodesplit]); err != nil {
		return err
	}
	if err := d.readLengths(d.mainlens[maincodesplit:d.mainElems]); err != nil {
		return err
	}
	d.hmain = buildTable(d.mainlens[:d.mainElems])
	if d.hmain == nil {
		return errCorrupt
	}

	if err := d.readLengths(d.lenlens[:]); err != nil {
		return err
	}
	d.hlen = buildTable(d.lenlens[:])
	if d.hlen == nil {
		return errCorrupt
	}
	return nil
}

// --- block decoding ---------------------------------------------------------

// readBlockHeader reads a block type and its 24-bit length, sets up the trees
// (or the uncompressed LRU), and updates the Intel-translation "started" flag.
// pos is the current output offset, recorded when translation first goes live.
func (d *lzxDecompressor) readBlockHeader(pos int) error {
	if d.unaligned {
		// Skip the pad byte left pending by an odd-length uncompressed block.
		if !d.haveInput(1) {
			return io.ErrUnexpectedEOF
		}
		d.inPos++
		d.unaligned = false
	}

	d.blockType = byte(d.getBits(3))
	hi := uint32(d.getBits(16))
	lo := uint32(d.getBits(8))
	d.blockLeft = hi<<8 | lo
	d.curBlockLen = d.blockLeft
	if d.eof {
		return io.ErrUnexpectedEOF
	}

	switch d.blockType {
	case lzxBlockVerbatim, lzxBlockAligned:
		if err := d.readTrees(d.blockType == lzxBlockAligned); err != nil {
			return err
		}
		// Translation only matters once a block able to emit a 0xE8 literal has
		// been seen; from that frame onward every frame is eligible.
		if !d.intelStarted && d.mainlens[0xE8] != 0 {
			d.intelStarted = true
			d.intelStart = pos
		}
	case lzxBlockUncompr:
		d.align16()
		var lru [12]byte
		if err := d.readBytes(lru[:]); err != nil {
			return err
		}
		d.R0 = binary.LittleEndian.Uint32(lru[0:4])
		d.R1 = binary.LittleEndian.Uint32(lru[4:8])
		d.R2 = binary.LittleEndian.Uint32(lru[8:12])
		if !d.intelStarted {
			d.intelStarted = true
			d.intelStart = pos
		}
	default:
		return errCorrupt
	}
	return nil
}

// decode runs the whole stream, returning outputLen bytes of translated output.
func (d *lzxDecompressor) decode(outputLen int) ([]byte, error) {
	d.out = make([]byte, outputLen)

	// The stream opens with the Intel E8 translation header: one flag bit and,
	// if set, a 32-bit file size read as two 16-bit halves.
	if !d.headerRead {
		if d.getBits(1) != 0 {
			hi := uint32(d.getBits(16))
			lo := uint32(d.getBits(16))
			d.intelSize = int32(hi<<16 | lo)
		}
		d.headerRead = true
	}

	pos := 0
	for pos < outputLen {
		// LZX blocks span frames freely, but the bitstream is flushed to a
		// 16-bit boundary at every 32 KiB frame, so decode one frame at a time
		// and realign in between.
		frameEnd := (pos/lzxFrameSize + 1) * lzxFrameSize
		if frameEnd > outputLen {
			frameEnd = outputLen
		}

		for pos < frameEnd {
			if d.blockLeft == 0 {
				if err := d.readBlockHeader(pos); err != nil {
					return nil, err
				}
			}
			run := frameEnd - pos
			if int(d.blockLeft) < run {
				run = int(d.blockLeft)
			}
			end := pos + run

			if d.blockType == lzxBlockUncompr {
				if err := d.readBytes(d.out[pos:end]); err != nil {
					return nil, err
				}
				d.blockLeft -= uint32(run)
				if d.blockLeft == 0 && d.curBlockLen%2 == 1 {
					d.unaligned = true
				}
				pos = end
				continue
			}

			np, err := d.decodeCompressed(pos, end, outputLen)
			if err != nil {
				return nil, err
			}
			pos = np
		}

		if pos < outputLen {
			d.alignFrame()
		}
	}

	// A truncation that getBits swallowed (e.g. in offset extra-bits) leaves the
	// output filled with garbage; surface it instead of returning success.
	if d.err != nil {
		return nil, d.err
	}

	d.translate(outputLen)
	return d.out, nil
}

// decodeCompressed decodes verbatim/aligned symbols until the output position
// reaches end, returning the new position. end is always a safe boundary (the
// block's end, or the total output length), so a valid stream never writes past
// it; the bounds checks guard only against corrupt input.
func (d *lzxDecompressor) decodeCompressed(pos, end, outputLen int) (int, error) {
	start := pos
	for pos < end {
		sym, err := d.getCode(d.hmain)
		if err != nil {
			return pos, err
		}
		if sym < lzxNumChars {
			d.out[pos] = byte(sym)
			pos++
			continue
		}

		length := int((sym - lzxNumChars) % 8)
		slot := int((sym - lzxNumChars) / 8)
		if length == lzxNumPrimaryLengths {
			extra, err := d.getCode(d.hlen)
			if err != nil {
				return pos, err
			}
			length += int(extra)
		}
		length += lzxMinMatch

		offset, err := d.matchOffset(slot)
		if err != nil {
			return pos, err
		}
		if offset > uint32(pos) || pos+length > outputLen {
			return pos, errCorrupt
		}
		src := pos - int(offset)
		if int(offset) >= length {
			// Non-overlapping: a single memmove-backed copy.
			copy(d.out[pos:pos+length], d.out[src:src+length])
		} else {
			// Overlapping run (offset < length): RLE-style byte fill.
			for i := 0; i < length; i++ {
				d.out[pos+i] = d.out[src+i]
			}
		}
		pos += length
	}
	// On valid input a block's symbols land exactly on its boundary; a corrupt
	// match that overruns the block would otherwise underflow blockLeft and make
	// the next block's header be skipped.
	produced := uint32(pos - start)
	if produced > d.blockLeft {
		return pos, errCorrupt
	}
	d.blockLeft -= produced
	return pos, nil
}

// matchOffset resolves a position slot into an absolute match offset, updating
// the repeated-offset LRU cache (R0/R1/R2).
func (d *lzxDecompressor) matchOffset(slot int) (uint32, error) {
	switch slot {
	case 0:
		return d.R0, nil
	case 1:
		off := d.R1
		d.R1 = d.R0
		d.R0 = off
		return off, nil
	case 2:
		off := d.R2
		d.R2 = d.R0
		d.R0 = off
		return off, nil
	}

	extra := lzxExtraBits[slot]
	off := lzxPositionBase[slot] - 2
	if extra >= 3 && d.blockType == lzxBlockAligned {
		if extra > 3 {
			off += uint32(d.getBits(extra-3)) << 3
		}
		a, err := d.getCode(d.halign)
		if err != nil {
			return 0, err
		}
		off += uint32(a)
	} else if extra > 0 {
		off += d.getBitsLong(extra)
	}
	d.R2 = d.R1
	d.R1 = d.R0
	d.R0 = off
	return off, nil
}

// translate applies the Intel E8 call-instruction fixup, frame by frame, to the
// output. The untranslated history stays intact because translation reads and
// writes only within each already-decoded frame.
func (d *lzxDecompressor) translate(outputLen int) {
	if d.intelSize == 0 || !d.intelStarted {
		return
	}
	startFrame := d.intelStart / lzxFrameSize
	for frame := 0; frame*lzxFrameSize < outputLen; frame++ {
		if frame >= e8FrameLimit || frame < startFrame {
			continue
		}
		start := frame * lzxFrameSize
		size := lzxFrameSize
		if outputLen-start < size {
			size = outputLen - start
		}
		if size <= 10 {
			continue
		}
		e8Translate(d.out[start:start+size], int32(start), d.intelSize)
	}
}

// e8Translate reverses the E8 (x86 CALL) preprocessing over one frame: absolute
// call targets in [-curpos, filesize) are converted back to relative offsets.
// The trailing 10 bytes of a frame are never translated.
func e8Translate(frame []byte, off, filesize int32) {
	for i := 0; i < len(frame)-10; i++ {
		if frame[i] != 0xE8 {
			continue
		}
		curpos := off + int32(i)
		abs := int32(binary.LittleEndian.Uint32(frame[i+1 : i+5]))
		if abs >= -curpos && abs < filesize {
			var rel int32
			if abs >= 0 {
				rel = abs - curpos
			} else {
				rel = abs + filesize
			}
			binary.LittleEndian.PutUint32(frame[i+1:i+5], uint32(rel))
		}
		i += 4
	}
}
