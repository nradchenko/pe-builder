// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"fmt"
	"strings"

	"github.com/nradchenko/pe-builder/inf"
)

// The source's hive-population INFs (hivesft.inf, hivedef.inf, hivecls.inf) are
// standard setupapi INF files. Their [AddReg] lines are
//
//	reg-root, subkey, value-name, flags, value...
//
// where flags is the setupapi FLG_ADDREG_* encoding — distinct from the REG_*
// codes plugin AddReg lines use. The low bits carry modifiers; the type lives in
// the high word, selected by the FLG_ADDREG_BINVALUETYPE (bit 0) family.
const (
	flgBinValueType = 0x00000001 // the value's type is in the binary family
	flgNoClobber    = 0x00000002 // keep an existing value; do not overwrite
	flgKeyOnly      = 0x00000010 // create the key only, write no value
)

// setupapiType maps a setupapi flags word to a REG_* value type. The high word
// carries the type; FLG_ADDREG_BINVALUETYPE (bit 0) selects the binary family.
// The three lowest type numbers of each family are special-cased; any larger
// high word is the REG_* type value directly (e.g. 0x000B0001 → REG_QWORD).
func setupapiType(flags int64) uint32 {
	hw := uint32((flags >> 16) & 0xFFFF)
	if flags&flgBinValueType != 0 {
		switch hw {
		case 0:
			return RegBinary
		case 1:
			return RegDWord
		case 2:
			return RegNone
		default:
			return hw
		}
	}
	switch hw {
	case 0:
		return RegSZ
	case 1:
		return RegMultiSZ
	case 2:
		return RegExpandSZ
	default:
		return hw
	}
}

// ApplyHiveINF parses a setupapi INF (its raw, possibly UTF-16, bytes) and
// applies its [AddReg] section to root. Only lines whose reg-root token matches
// wantRoot are used; stripPrefix is removed from the head of each subkey path so
// the hive root maps to the right place (e.g. the SOFTWARE hive's root is
// HKLM\SOFTWARE, so "SOFTWARE" is stripped). Values are last-writer-wins unless
// the line carries the NOCLOBBER modifier.
//
// Only [AddReg] is applied: the [DelReg] sections of these INFs remove keys
// during an in-place upgrade and have nothing to act on when populating a fresh
// base hive.
func ApplyHiveINF(root *Key, data []byte, wantRoot, stripPrefix string) error {
	f, err := inf.Parse(decodeINF(data))
	if err != nil {
		return fmt.Errorf("hive: parse INF: %w", err)
	}
	for _, ln := range f.Section("AddReg", 0) {
		applyAddRegLine(root, ln, wantRoot, stripPrefix)
	}
	return nil
}

// applyAddRegLine applies a single [AddReg] directive to the tree.
func applyAddRegLine(root *Key, ln *inf.Line, wantRoot, stripPrefix string) {
	fields := ln.Fields()
	if len(fields) < 2 {
		return // need at least a reg-root and a subkey
	}
	if !strings.EqualFold(fields[0], wantRoot) {
		return // a different root (e.g. the lone HKLM anchor in hivecls.inf)
	}

	key := root.EnsurePath(stripRoot(fields[1], stripPrefix))
	if len(fields) < 3 {
		return // no value-name field: create the key only, matching setupapi
	}

	var flags int64
	if v, ok := ln.IntField(3); ok {
		flags = v
	}
	if flags&flgKeyOnly != 0 {
		return // key already ensured; no value to write
	}

	valueName := fields[2]
	if flags&flgNoClobber != 0 {
		if _, exists := key.Value(valueName); exists {
			return
		}
	}
	typ := setupapiType(flags)
	key.SetValue(Value{Name: valueName, Type: typ, Data: encodeAddRegData(typ, ln)})
}

// encodeAddRegData encodes an [AddReg] line's value fields (index 4 onward) into
// the on-disk bytes for the given type.
func encodeAddRegData(typ uint32, ln *inf.Line) []byte {
	fields := ln.Fields()
	switch typ {
	case RegSZ, RegExpandSZ:
		return EncodeSZ(fieldOr(fields, 4))
	case RegMultiSZ:
		if len(fields) > 4 {
			return EncodeMultiSZ(fields[4:])
		}
		return EncodeMultiSZ(nil)
	case RegDWord:
		v, _ := ln.IntField(4)
		return EncodeDWord(uint32(v))
	default:
		// REG_BINARY, REG_NONE, REG_QWORD, and any other type carry their data
		// as comma-separated hex bytes.
		b, _ := ln.BinaryFields(4)
		return b
	}
}

// stripRoot removes a leading "prefix\" (or an exact prefix) from a subkey path,
// case-insensitively. A path that does not carry the prefix is returned as-is.
func stripRoot(path, prefix string) string {
	if prefix == "" {
		return path
	}
	if strings.EqualFold(path, prefix) {
		return ""
	}
	p := prefix + `\`
	if len(path) >= len(p) && strings.EqualFold(path[:len(p)], p) {
		return path[len(p):]
	}
	return path
}

// fieldOr returns fields[i], or "" when it is absent.
func fieldOr(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
}

// decodeINF returns INF text as bytes the parser can read, decoding a UTF-16
// byte-order-marked file (the source's hive*.inf are UTF-16LE) to UTF-8 and
// passing ASCII/UTF-8 through unchanged.
func decodeINF(data []byte) []byte {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return []byte(decodeUTF16(data[2:]))
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return []byte(decodeUTF16BE(data[2:]))
	default:
		return data
	}
}

// decodeUTF16BE decodes big-endian UTF-16 bytes to a Go string.
func decodeUTF16BE(b []byte) string {
	swapped := make([]byte, len(b))
	for i := 0; i+1 < len(b); i += 2 {
		swapped[i] = b[i+1]
		swapped[i+1] = b[i]
	}
	return decodeUTF16(swapped)
}
