// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0
//
// MSZIP is the cabinet format's deflate-based compression method: every CFDATA
// block carries a "CK" signature followed by a raw deflate stream of at most one
// 32 KiB frame. Unlike LZX, whose bitstream runs continuously across blocks, each
// MSZIP block is a self-contained deflate stream — but it is compressed against
// the previous block's uncompressed output as a preset dictionary, so the blocks
// must still be decoded in order and cannot be decoded independently.
//
// The deflate decoding itself is the standard library's; only the per-block
// framing and the dictionary carry-over are implemented here.

package compress

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"
)

// mszipSignature precedes the deflate stream in every MSZIP CFDATA block.
var mszipSignature = []byte{'C', 'K'}

// mszipWindowSize is deflate's maximum match distance, and so the reach of the
// dictionary a block may refer back into. It happens to equal the cabinet's
// 32 KiB frame size, but it is a property of deflate rather than of the
// container: a match may reach this far back regardless of where block
// boundaries fall.
const mszipWindowSize = 32768

// mszipDecode decompresses a folder's CFDATA blocks, whose payloads are given in
// archive order along with each block's declared uncompressed length. The result
// is the concatenation of the blocks' output, totalling uncompLen bytes.
func mszipDecode(blocks [][]byte, uncompLen []int, total int) ([]byte, error) {
	out := make([]byte, 0, total)
	for i, block := range blocks {
		if !bytes.HasPrefix(block, mszipSignature) {
			return nil, fmt.Errorf("MSZIP block %d: missing CK signature", i)
		}
		// Everything decoded so far, capped to deflate's reach, is this block's
		// preset dictionary. It is emphatically not just the previous block: when
		// a block is short, a match may reach back over it into earlier ones. That
		// distinction is invisible in a makecab-produced cabinet, which fills every
		// block but the last, making the two readings identical — see
		// TestMSZIPShortBlockWindow. flate copies the dictionary into its own
		// history at construction, so handing it a view of out is safe as out grows.
		var dict []byte
		if n := len(out); n > mszipWindowSize {
			dict = out[n-mszipWindowSize:]
		} else {
			dict = out
		}
		r := flate.NewReaderDict(bytes.NewReader(block[len(mszipSignature):]), dict)

		// Read exactly the declared length rather than to EOF: some MSZIP encoders
		// omit deflate's final-block marker, so the stream ends without an explicit
		// terminator and cbUncomp is the authoritative length.
		start := len(out)
		out = append(out, make([]byte, uncompLen[i])...)
		if _, err := io.ReadFull(r, out[start:]); err != nil {
			return nil, fmt.Errorf("MSZIP block %d: %w", i, err)
		}

		// Reject a block that decodes to more than it declared. Left unchecked, the
		// surplus is dropped and the truncated output becomes the next block's
		// dictionary, so the whole rest of the folder decodes to garbage and is
		// still returned as success. Only a byte actually produced counts: a read
		// that merely fails is the tolerated missing-final-block case above, not
		// evidence of extra output.
		var extra [1]byte
		if _, err := io.ReadFull(r, extra[:]); err == nil {
			return nil, fmt.Errorf("MSZIP block %d: decodes to more than the declared %d bytes",
				i, uncompLen[i])
		}
		if err := r.Close(); err != nil {
			return nil, fmt.Errorf("MSZIP block %d: %w", i, err)
		}
	}
	return out, nil
}
