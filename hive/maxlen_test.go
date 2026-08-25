// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"bytes"
	"testing"
	"unicode/utf16"

	"www.velocidex.com/golang/regparser"
)

// The property both tests below check is "never under-report": a key's stored maxima must
// be at least as large as the names and data it actually holds. Windows reports them
// through RegQueryInfoKey, and a caller that sizes an enumeration buffer from them fails
// with ERROR_MORE_DATA if they come back short — which is what reg.exe does on a hive
// whose maxima were left zero. They are high-water marks, so a value larger than anything
// currently present is legitimate and only the shortfall is a defect.

// checkMaxima walks every key of a written hive and fails for each one whose stored maxima
// are smaller than the names and data that key actually holds.
func checkMaxima(t *testing.T, data []byte, label string) {
	t.Helper()
	reg, err := regparser.NewRegistry(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: NewRegistry: %v", label, err)
	}
	var walk func(k *regparser.CM_KEY_NODE, path string)
	checked := 0
	walk = func(k *regparser.CM_KEY_NODE, path string) {
		checked++
		var wantName, wantData, wantSubkey uint32
		for _, v := range k.Values() {
			// Decode through the package's own helpers: regparser hands back the raw
			// on-disk name bytes, which are Latin-1 or UTF-16LE depending on the record's
			// compression flag, and measuring those directly counts a UTF-16 name twice.
			if n := utf16Len(valueName(v)); n > wantName {
				wantName = n
			}
			// DataSize, not len(ValueData().Data): regparser returns a fixed four bytes
			// for a value stored inline in its vk record, whatever its real size.
			if n := uint32(v.DataSize()); n > wantData {
				wantData = n
			}
		}
		subkeys := k.Subkeys()
		for _, c := range subkeys {
			if n := utf16Len(nodeName(c)); n > wantSubkey {
				wantSubkey = n
			}
		}
		if got := uint32(k.MaxNameLen()); got < wantSubkey {
			t.Errorf("%s: %s: MaxNameLen = %d, shorter than its longest subkey name (%d bytes)", label, path, got, wantSubkey)
		}
		if got := k.MaxValueNameLen(); got < wantName {
			t.Errorf("%s: %s: MaxValueNameLen = %d, shorter than its longest value name (%d bytes)", label, path, got, wantName)
		}
		if got := k.MaxValueDataLen(); got < wantData {
			t.Errorf("%s: %s: MaxValueDataLen = %d, shorter than its largest value (%d bytes)", label, path, got, wantData)
		}
		for _, c := range subkeys {
			walk(c, path+`\`+c.Name())
		}
	}
	root := reg.OpenKey(`\`)
	if root == nil {
		t.Fatalf("%s: OpenKey(root) returned nil", label)
	}
	walk(root, root.Name())
	if checked == 0 {
		t.Fatalf("%s: walked no keys", label)
	}
	t.Logf("%s: checked %d keys", label, checked)
}

// utf16Len is the length of a name in UTF-16 bytes, the unit the nk maxima count in.
func utf16Len(s string) uint32 { return uint32(2 * len(utf16.Encode([]rune(s)))) }

// TestWrittenHiveReportsMaxima checks our own writer against the property.
func TestWrittenHiveReportsMaxima(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	checkMaxima(t, data, "written")
}

// TestMicrosoftHiveReportsMaxima pins the convention to the real criterion: the same
// property, checked against a hive Windows itself wrote. It is what settles that the
// maxima count UTF-16 bytes rather than characters — a hive of Microsoft's would fail
// this test if the unit were anything else.
func TestMicrosoftHiveReportsMaxima(t *testing.T) {
	b := sourceBacking(t) // skips when the source ISO is absent
	checkMaxima(t, sourceFile(t, b, "setupreg.hiv"), "setupreg.hiv (Microsoft-written)")
}

// TestMaximaShortValuesAndWideNames covers the two shapes the sample hive happens not to
// have: a key whose largest value is shorter than the four bytes a vk record stores inline,
// and names that do not fit the compressed one-byte-per-character form. Both are cases where
// a careless reading of the hive claims more than the writer stored.
func TestMaximaShortValuesAndWideNames(t *testing.T) {
	h := NewHive("ROOT")
	short := h.Root.EnsureChild("Short")
	short.SetValue(Value{Name: "OneByte", Type: RegBinary, Data: []byte{0x7}})
	short.SetValue(Value{Name: "Nothing", Type: RegBinary, Data: nil})

	wide := h.Root.EnsureChild("Ωmega")
	wide.SetValue(Value{Name: "Ωvalue", Type: RegDWord, Data: EncodeDWord(1)})

	data, err := WriteHive(h)
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	checkMaxima(t, data, "short values and wide names")
}
