// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"bytes"
	"testing"
)

// A small ASCII setupapi INF exercising each value type, a default value,
// key-only creation, the SOFTWARE-prefix strip, and the NOCLOBBER modifier.
const sampleAddRegINF = `[Version]
Signature="$Windows NT$"

[AddReg]
HKLM,"SOFTWARE\Test","StringVal",0x00000000,"hello"
HKLM,"SOFTWARE\Test","ExpandVal",0x00020000,"%SystemRoot%\x"
HKLM,"SOFTWARE\Test","DwordVal",0x00010001,0x2a
HKLM,"SOFTWARE\Test","MultiVal",0x00010000,"a","b","c"
HKLM,"SOFTWARE\Test","BinVal",0x00000001,de,ad,be,ef
HKLM,"SOFTWARE\Test","QwordVal",0x000B0001,11,22,33,44,55,66,77,88
HKLM,"SOFTWARE\Test",,0x00000000,"defaultdata"
HKLM,"SOFTWARE\KeyOnly",,0x00000010
HKLM,"SOFTWARE\BareKey"
HKLM,"SOFTWARE\Test","Keep",0x00000000,"first"
HKLM,"SOFTWARE\Test","Keep",0x00000002,"second"
HKCU,"ShouldBeIgnored","x",0x00000000,"y"
`

func TestApplyHiveINFEncoding(t *testing.T) {
	root := &Key{Name: "ROOT"}
	if err := ApplyHiveINF(root, []byte(sampleAddRegINF), "HKLM", "SOFTWARE"); err != nil {
		t.Fatal(err)
	}

	if root.Child("SOFTWARE") != nil {
		t.Error("SOFTWARE prefix was not stripped")
	}
	if root.Child("ShouldBeIgnored") != nil {
		t.Error("a non-HKLM (HKCU) line was applied")
	}
	if root.Child("KeyOnly") == nil {
		t.Error("key-only directive did not create the key")
	}
	if k := root.Child("KeyOnly"); k != nil && len(k.Values) != 0 {
		t.Error("key-only directive wrote a value")
	}
	// A line with no value-name field (root + subkey only) creates the key alone.
	if bk := root.Child("BareKey"); bk == nil {
		t.Error("bare key line did not create the key")
	} else if len(bk.Values) != 0 {
		t.Errorf("bare key line wrote %d values, want 0 (key only)", len(bk.Values))
	}

	test := root.Child("Test")
	if test == nil {
		t.Fatal("Test key missing")
	}

	wantString := func(name, want string) {
		t.Helper()
		v, ok := test.Value(name)
		if !ok {
			t.Errorf("%s missing", name)
			return
		}
		if got := v.AsString(); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	wantString("StringVal", "hello")
	wantString("ExpandVal", `%SystemRoot%\x`)
	wantString("", "defaultdata") // the key's default value

	if v, _ := test.Value("StringVal"); v.Type != RegSZ {
		t.Errorf("StringVal type = %d, want REG_SZ", v.Type)
	}
	if v, _ := test.Value("ExpandVal"); v.Type != RegExpandSZ {
		t.Errorf("ExpandVal type = %d, want REG_EXPAND_SZ", v.Type)
	}

	dw, ok := test.Value("DwordVal")
	if !ok || dw.Type != RegDWord {
		t.Fatalf("DwordVal type = %d, want REG_DWORD", dw.Type)
	}
	if dw.AsDWord() != 0x2a || !bytes.Equal(dw.Data, []byte{0x2a, 0, 0, 0}) {
		t.Errorf("DwordVal = %v, want 0x2a little-endian", dw.Data)
	}

	mv, _ := test.Value("MultiVal")
	if mv.Type != RegMultiSZ {
		t.Errorf("MultiVal type = %d, want REG_MULTI_SZ", mv.Type)
	}
	if want := EncodeMultiSZ([]string{"a", "b", "c"}); !bytes.Equal(mv.Data, want) {
		t.Errorf("MultiVal = %v, want %v", mv.Data, want)
	}

	bv, _ := test.Value("BinVal")
	if bv.Type != RegBinary || !bytes.Equal(bv.Data, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("BinVal = %v (type %d), want de ad be ef REG_BINARY", bv.Data, bv.Type)
	}

	// A high-word setupapi type (0x000B0001) must decode to REG_QWORD with its
	// raw eight bytes preserved, not collapse to REG_NONE with the data dropped.
	qv, _ := test.Value("QwordVal")
	if qv.Type != RegQWord {
		t.Errorf("QwordVal type = %d, want REG_QWORD (%d)", qv.Type, RegQWord)
	}
	if want := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}; !bytes.Equal(qv.Data, want) {
		t.Errorf("QwordVal = %v, want %v", qv.Data, want)
	}

	// NOCLOBBER: the second "Keep" line must not overwrite the first.
	keep, _ := test.Value("Keep")
	if keep.AsString() != "first" {
		t.Errorf("Keep = %q, want %q (NOCLOBBER should preserve the first write)", keep.AsString(), "first")
	}
}

func TestSetupapiType(t *testing.T) {
	cases := []struct {
		flags int64
		want  uint32
	}{
		{0x00000000, RegSZ},
		{0x00000001, RegBinary},
		{0x00010000, RegMultiSZ},
		{0x00010001, RegDWord},
		{0x00020000, RegExpandSZ},
		{0x00020001, RegNone},
		{0x00000012, RegSZ},     // KEYONLY|NOCLOBBER: modifier bits don't change the type
		{0x00030001, RegBinary}, // high word 3 with bit 0: REG_BINARY (real hivesft.inf)
		{0x00030003, RegBinary}, // same, with NOCLOBBER
		{0x000B0001, RegQWord},  // high word 11: REG_QWORD (real hivesft.inf)
		{0x00020002, RegExpandSZ},
	}
	for _, c := range cases {
		if got := setupapiType(c.flags); got != c.want {
			t.Errorf("setupapiType(%#x) = %d, want %d", c.flags, got, c.want)
		}
	}
}

func TestDecodeINFUTF16(t *testing.T) {
	// "[X]\nk=v" as UTF-16LE with a BOM decodes to the ASCII text.
	src := "[X]\nk=v"
	u16 := []byte{0xFF, 0xFE}
	for _, r := range src {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	if got := string(decodeINF(u16)); got != src {
		t.Errorf("decodeINF(UTF-16LE) = %q, want %q", got, src)
	}
	if got := string(decodeINF([]byte(src))); got != src {
		t.Errorf("decodeINF(ASCII) = %q, want %q", got, src)
	}
}
