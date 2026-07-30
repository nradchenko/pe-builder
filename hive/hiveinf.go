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
//
// Limitation: setupapi [Strings] macros in value data are not resolved — values are
// stored verbatim. This is correct for REG_EXPAND_SZ env refs (%SystemRoot% is meant to
// survive to runtime) but leaves a true [Strings] macro such as %UNKNOWN% (a device-class
// display name) literal. Cosmetic; a proper [Strings] pass in the inf parser is a follow-up.
func ApplyHiveINF(root *Key, data []byte, wantRoot, stripPrefix string) error {
	f, err := inf.Parse(data)
	if err != nil {
		return fmt.Errorf("hive: parse INF: %w", err)
	}
	for _, ln := range f.Section("AddReg", 0) {
		applyAddRegLine(root, ln, wantRoot, stripPrefix, nil, false, nil)
	}
	return nil
}

// ApplySystemINF folds the source's hivesys.inf (the full GUI-mode system config) onto an
// existing SYSTEM hive root — the standard config a bare text-setup setupreg.hiv lacks and the
// Service Control Manager needs to complete startup. It maps the INF's SYSTEM\CurrentControlSet
// paths onto the offline hive's real ControlSet001 (via remap), preserves any value already
// present so the text-setup boot config is never overwritten (global no-clobber), and skips lines
// whose key is not under SYSTEM (hivesys.inf also carries some SOFTWARE\… lines that belong to the
// SOFTWARE hive, not this one).
//
// Start values need care: hivesys.inf carries per-hardware DISABLED defaults (Start=4) on
// boot/storage DRIVERS that real GUI setup overrides, and folding those onto our txtsetup-driven
// boot config would disable boot drivers (0x7B). So driver (Type 1 kernel / 2 filesystem) Starts
// are dropped — boot drivers load via txtsetup.sif and the reference build likewise ships them
// Start-less — while every non-driver service keeps its Start so the SCM auto-starts the standard
// service set (win32 own/shared 0x10/0x20 and their interactive 0x110/0x120 variants included).
func ApplySystemINF(root *Key, data []byte, remap func(string) string) error {
	f, err := inf.Parse(data)
	if err != nil {
		return fmt.Errorf("hive: parse INF: %w", err)
	}
	// Only HKLM lines under SYSTEM belong in this hive; skip the INF's SOFTWARE\… lines.
	var lines []*inf.Line
	for _, ln := range f.Section("AddReg", 0) {
		fields := ln.Fields()
		if len(fields) >= 2 && strings.EqualFold(fields[0], "HKLM") && under(fields[1], "SYSTEM") {
			lines = append(lines, ln)
		}
	}
	// Pass 1: which Services\<name> are DRIVERS (Type 1 or 2)? Only their Start is dropped.
	driver := map[string]bool{}
	for _, ln := range lines {
		fields := ln.Fields()
		if len(fields) < 5 || !strings.EqualFold(fields[2], "Type") {
			continue
		}
		name, ok := serviceNameFromPath(stripRoot(fields[1], "SYSTEM"))
		if !ok {
			continue
		}
		if t, ok := ln.IntField(4); ok && (t == 1 || t == 2) {
			driver[name] = true
		}
	}
	// Pass 2: apply, dropping only driver Starts.
	for _, ln := range lines {
		applyAddRegLine(root, ln, "HKLM", "SYSTEM", remap, true, driver)
	}
	return nil
}

// under reports whether subkey path p sits at or under prefix (case-insensitive).
func under(p, prefix string) bool {
	if strings.EqualFold(p, prefix) {
		return true
	}
	q := prefix + `\`
	return len(p) >= len(q) && strings.EqualFold(p[:len(q)], q)
}

// serviceNameFromPath returns the lower-cased service name from a control-set-relative path
// like "ControlSet001\Services\Foo" or "ControlSet001\Services\Foo\Parameters".
func serviceNameFromPath(path string) (string, bool) {
	parts := strings.Split(path, `\`)
	if len(parts) >= 3 && strings.EqualFold(parts[1], "Services") &&
		(strings.EqualFold(parts[0], "ControlSet001") || strings.EqualFold(parts[0], "CurrentControlSet")) {
		return strings.ToLower(parts[2]), true
	}
	return "", false
}

// applyAddRegLine applies a single [AddReg] directive to the tree. dropStart names the driver
// services whose "Start" value must be dropped (nil drops none); see ApplySystemINF.
func applyAddRegLine(root *Key, ln *inf.Line, wantRoot, stripPrefix string, remap func(string) string, globalNoClobber bool, dropStart map[string]bool) {
	fields := ln.Fields()
	if len(fields) < 2 {
		return // need at least a reg-root and a subkey
	}
	if !strings.EqualFold(fields[0], wantRoot) {
		return // a different root (e.g. the lone HKLM anchor in hivecls.inf)
	}

	path := stripRoot(fields[1], stripPrefix)
	if remap != nil {
		path = remap(path)
	}
	key := root.EnsurePath(path)
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
	if dropStart != nil && strings.EqualFold(valueName, "Start") {
		// Drop a Start only for a known driver service; hivesys.inf's per-hardware disabled
		// defaults would break the txtsetup-driven boot config. Non-driver services, services
		// not classified here, and non-service "Start" values keep their value.
		if name, ok := serviceNameFromPath(path); ok && dropStart[name] {
			return
		}
	}
	if flags&flgNoClobber != 0 || globalNoClobber {
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
