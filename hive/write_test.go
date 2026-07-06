// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/nradchenko/pe-builder/internal/golden"
)

// sampleHive builds a small tree exercising every value type, a default value,
// nested keys, an empty key, and a value with no data.
func sampleHive() *Hive {
	h := NewHive("ROOT")
	ctrl := h.Root.EnsurePath(`ControlSet001\Control`)
	ctrl.SetValue(Value{Name: "", Type: RegSZ, Data: EncodeSZ("default")})
	cn := ctrl.EnsureChild("ComputerName").EnsureChild("ComputerName")
	cn.SetValue(Value{Name: "ComputerName", Type: RegSZ, Data: EncodeSZ("MINIPE")})

	svc := h.Root.EnsurePath(`ControlSet001\Services\Foo`)
	svc.SetValue(Value{Name: "Start", Type: RegDWord, Data: EncodeDWord(1)})
	svc.SetValue(Value{Name: "Group", Type: RegExpandSZ, Data: EncodeSZ("%SystemRoot%\\x")})
	svc.SetValue(Value{Name: "DependOnService", Type: RegMultiSZ, Data: EncodeMultiSZ([]string{"RpcSs", "Tcpip"})})
	svc.SetValue(Value{Name: "Blob", Type: RegBinary, Data: []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}})
	svc.SetValue(Value{Name: "Empty", Type: RegBinary, Data: nil})

	h.Root.EnsureChild("Select").SetValue(Value{Name: "Default", Type: RegDWord, Data: EncodeDWord(1)})
	h.Root.EnsureChild("EmptyKey")
	return h
}

// sortTree orders every subkey list case-insensitively, matching the writer's
// canonical order so a hand-built tree compares equal to a round-tripped one.
func sortTree(k *Key) {
	sort.SliceStable(k.Subkeys, func(i, j int) bool {
		return upperASCII(k.Subkeys[i].Name) < upperASCII(k.Subkeys[j].Name)
	})
	for _, c := range k.Subkeys {
		sortTree(c)
	}
}

func TestRoundTripSmall(t *testing.T) {
	in := sampleHive()
	data, err := WriteHive(in)
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	out, err := ReadHive(data)
	if err != nil {
		t.Fatalf("ReadHive: %v", err)
	}
	sortTree(in.Root)
	sortTree(out.Root)
	if diff := cmp.Diff(in, out, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("round-trip mismatch (-in +out):\n%s", diff)
	}
}

func TestRoundTripSetupreg(t *testing.T) {
	in := setupregHive(t) // skips when the ISO is absent
	data, err := WriteHive(in)
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	out, err := ReadHive(data)
	if err != nil {
		t.Fatalf("ReadHive: %v", err)
	}
	sortTree(in.Root)
	sortTree(out.Root)
	if diff := cmp.Diff(in, out, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("setupreg round-trip mismatch (-in +out):\n%s", diff)
	}
}

func TestWriteGolden(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	golden.AssertBytes(t, "sample-hive.regf", data)
}

// TestHivexValidatesOutput cross-checks that an independent Windows-compatible
// reader (libhivex, via hivexml) accepts our output — the key pre-boot signal.
func TestHivexValidatesOutput(t *testing.T) {
	hivexml, err := exec.LookPath("hivexml")
	if err != nil {
		t.Skip("hivexml not installed; skipping cross-validation")
	}
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sample.hiv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(hivexml, path).CombinedOutput()
	if err != nil {
		t.Fatalf("hivexml rejected our hive: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Error("hivexml produced no output")
	}
}

// TestHivexValidatesSetupreg re-serializes the real SYSTEM hive and confirms
// libhivex accepts the full-scale result, exercising the writer on thousands of
// keys and every value type a real hive carries.
func TestHivexValidatesSetupreg(t *testing.T) {
	hivexml, err := exec.LookPath("hivexml")
	if err != nil {
		t.Skip("hivexml not installed; skipping cross-validation")
	}
	data, err := WriteHive(setupregHive(t)) // skips when the ISO is absent
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	path := filepath.Join(t.TempDir(), "system.hiv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(hivexml, path).CombinedOutput(); err != nil {
		t.Fatalf("hivexml rejected our SYSTEM hive: %v\n%s", err, out)
	}
}

// TestRoundTripDataStartingDB guards against a reader misparsing a small value
// whose bytes begin 0x64,0x62 ("db") as a big-data record: such values must be
// stored inline, so they round-trip intact.
func TestRoundTripDataStartingDB(t *testing.T) {
	h := NewHive("ROOT")
	k := h.Root.EnsureChild("K")
	k.SetValue(Value{Name: "dw", Type: RegDWord, Data: EncodeDWord(0x00006264)}) // bytes 64 62 00 00
	k.SetValue(Value{Name: "bin4", Type: RegBinary, Data: []byte{0x64, 0x62, 0x11, 0x22}})
	data, err := WriteHive(h)
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	out, err := ReadHive(data)
	if err != nil {
		t.Fatalf("ReadHive: %v", err)
	}
	ko := out.Root.Child("K")
	if dw, _ := ko.Value("dw"); dw.AsDWord() != 0x00006264 {
		t.Errorf("DWORD round-trip corrupted: got %x", dw.Data)
	}
	if bin, _ := ko.Value("bin4"); !cmp.Equal(bin.Data, []byte{0x64, 0x62, 0x11, 0x22}) {
		t.Errorf("BINARY round-trip corrupted: got %x", bin.Data)
	}
}

// TestRoundTripNonASCIINames covers both name encodings: an extended-Latin-1
// name (every rune ≤ 0xFF, stored one byte per character as a compressed name)
// and a name needing UTF-16 (a rune > 0xFF). Both must round-trip byte-for-byte
// rather than come back as mojibake.
func TestRoundTripNonASCIINames(t *testing.T) {
	const latin1Key = "Résumé" // é = U+00E9, stored compressed as byte 0xE9
	const latin1Val = "Größe"  // ö = U+00F6, ß = U+00DF, all ≤ 0xFF
	const wideKey = "Привет"   // Cyrillic, > 0xFF, forces UTF-16

	h := NewHive("ROOT")
	k := h.Root.EnsureChild(latin1Key)
	k.SetValue(Value{Name: latin1Val, Type: RegSZ, Data: EncodeSZ("x")})
	h.Root.EnsureChild(wideKey)

	data, err := WriteHive(h)
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	out, err := ReadHive(data)
	if err != nil {
		t.Fatalf("ReadHive: %v", err)
	}

	rk := out.Root.Child(latin1Key)
	if rk == nil {
		t.Fatalf("Latin-1 key %q not found after round-trip", latin1Key)
	}
	if rk.Name != latin1Key {
		t.Errorf("Latin-1 key name = %q, want %q", rk.Name, latin1Key)
	}
	if _, ok := rk.Value(latin1Val); !ok {
		t.Errorf("Latin-1 value name %q not found after round-trip", latin1Val)
	}
	if out.Root.Child(wideKey) == nil || out.Root.Child(wideKey).Name != wideKey {
		t.Errorf("wide (UTF-16) key %q did not round-trip", wideKey)
	}

	// An independent reader must also accept the result.
	if hivexml, err := exec.LookPath("hivexml"); err == nil {
		path := filepath.Join(t.TempDir(), "names.hiv")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if o, err := exec.Command(hivexml, path).CombinedOutput(); err != nil {
			t.Errorf("hivexml rejected a hive with non-ASCII names: %v\n%s", err, o)
		}
	}
}

func TestBigDataGuard(t *testing.T) {
	h := NewHive("ROOT")
	h.Root.SetValue(Value{Name: "big", Type: RegBinary, Data: make([]byte, maxCellData+1)})
	if _, err := WriteHive(h); err == nil {
		t.Fatal("expected big-data (db) guard error, got nil")
	}
}

func TestSubkeyRootGuard(t *testing.T) {
	h := NewHive("ROOT")
	for i := 0; i <= maxSubkeys; i++ {
		h.Root.Subkeys = append(h.Root.Subkeys, &Key{Name: "k" + strconv.Itoa(i)})
	}
	if _, err := WriteHive(h); err == nil {
		t.Fatal("expected subkey-root (ri) guard error, got nil")
	}
}
