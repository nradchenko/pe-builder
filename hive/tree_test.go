// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import "testing"

func TestChildCaseInsensitive(t *testing.T) {
	root := &Key{Name: "root"}
	sel := root.EnsureChild("Select")
	if got := root.Child("select"); got != sel {
		t.Errorf("Child(%q) = %v, want the created key", "select", got)
	}
	if got := root.Child("SELECT"); got != sel {
		t.Errorf("Child(%q) = %v, want the created key", "SELECT", got)
	}
	if got := root.Child("missing"); got != nil {
		t.Errorf("Child(missing) = %v, want nil", got)
	}
}

func TestEnsureChildCreateOrGet(t *testing.T) {
	root := &Key{Name: "root"}
	a := root.EnsureChild("ControlSet001")
	b := root.EnsureChild("controlset001") // same key, different case
	if a != b {
		t.Fatal("EnsureChild created a duplicate for a case variant")
	}
	if len(root.Subkeys) != 1 {
		t.Fatalf("root has %d subkeys, want 1", len(root.Subkeys))
	}
	if root.Subkeys[0].Name != "ControlSet001" {
		t.Errorf("stored name = %q, want the first-seen casing %q", root.Subkeys[0].Name, "ControlSet001")
	}
}

func TestEnsurePath(t *testing.T) {
	root := &Key{Name: "root"}
	leaf := root.EnsurePath(`ControlSet001\Control\ComputerName`)
	if leaf.Name != "ComputerName" {
		t.Fatalf("leaf name = %q, want ComputerName", leaf.Name)
	}
	// Re-walking with mixed case and stray separators reaches the same leaf.
	again := root.EnsurePath(`\controlset001\CONTROL\computername\`)
	if again != leaf {
		t.Errorf("EnsurePath returned a different key for a case/separator variant")
	}
	if got := root.Child("ControlSet001").Child("Control").Child("ComputerName"); got != leaf {
		t.Errorf("path not wired through intermediate keys")
	}
}

func TestSetValueLastWriterWins(t *testing.T) {
	k := &Key{Name: "k"}
	k.SetValue(Value{Name: "Start", Type: 4, Data: []byte{1, 0, 0, 0}})
	k.SetValue(Value{Name: "start", Type: 4, Data: []byte{4, 0, 0, 0}}) // replace
	if len(k.Values) != 1 {
		t.Fatalf("have %d values, want 1 after replace", len(k.Values))
	}
	got, ok := k.Value("START")
	if !ok {
		t.Fatal("value not found by case-folded name")
	}
	if got.Data[0] != 4 {
		t.Errorf("value data = %v, want the replacement (last writer wins)", got.Data)
	}
	if got.Name != "Start" {
		t.Errorf("stored value name = %q, want first-seen casing %q", got.Name, "Start")
	}
}

func TestDefaultValue(t *testing.T) {
	k := &Key{Name: "k"}
	k.SetValue(Value{Name: "", Type: 1, Data: EncodeSZ("shell")})
	got, ok := k.Value("")
	if !ok {
		t.Fatal("default value not found")
	}
	if got.Type != 1 {
		t.Errorf("default value type = %d, want 1", got.Type)
	}
}

func TestReadSetupregSystemKeys(t *testing.T) {
	h := setupregHive(t)
	// The SYSTEM base hive must expose its well-known top-level structure.
	if h.Root.Child("Select") == nil {
		t.Error("missing Select key")
	}
	cs1 := h.Root.Child("ControlSet001")
	if cs1 == nil {
		t.Fatal("missing ControlSet001")
	}
	if cs1.Child("Control") == nil {
		t.Error("missing ControlSet001\\Control")
	}
	if cs1.Child("Services") == nil {
		t.Error("missing ControlSet001\\Services")
	}
}
