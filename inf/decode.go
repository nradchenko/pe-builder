// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import (
	"encoding/binary"
	"unicode/utf16"
)

// Decode returns INF text as bytes the parser reads, converting a byte-order-marked
// UTF-16 file to UTF-8 and passing everything else through unchanged. A Windows
// source mixes encodings — the hive-population INFs (hivesys.inf and friends) are
// UTF-16LE, while txtsetup.sif, layout.inf and the driver INFs are single-byte
// ANSI — so every reader has to cope with both. Parse applies this itself; call it
// directly only when the decoded text is wanted for something other than parsing.
//
// Only a BOM selects UTF-16: a BOM-less file is single-byte text of some code page,
// which is passed through byte-for-byte (the parser interprets only ASCII
// metacharacters, so a high-bit byte survives into the field it belongs to).
//
// The result may alias data: a single-byte file is returned as-is, and a UTF-8 BOM is
// dropped by reslicing, while either UTF-16 form is freshly allocated. A caller that
// writes to the result — appending to it, or editing it in place — must copy first, or
// it corrupts the caller's buffer for exactly the encodings that are not converted.
func Decode(data []byte) []byte {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], binary.LittleEndian)
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], binary.BigEndian)
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		// Already UTF-8; only the marker has to go, or it would be read as the
		// first characters of the first directive.
		return data[3:]
	default:
		return data
	}
}

// decodeUTF16 decodes UTF-16 code units of the given byte order to UTF-8. A trailing
// odd byte (never expected in a well-formed file) is ignored.
func decodeUTF16(b []byte, order binary.ByteOrder) []byte {
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u16 = append(u16, order.Uint16(b[i:]))
	}
	return []byte(string(utf16.Decode(u16)))
}
