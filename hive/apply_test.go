// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"testing"

	"github.com/nradchenko/pe-builder/internal/action"
)

// newHives returns a set of empty hives for exercising Apply.
func newHives() *Hives {
	return &Hives{
		System:   NewHive("$$$PROTO.HIV"),
		Software: NewHive("$$$PROTO.HIV"),
		Default:  NewHive("$$$PROTO.HIV"),
	}
}

func TestApplyAddReg(t *testing.T) {
	h := newHives()
	acts := []action.Action{
		// key-only marker
		{Kind: action.AddReg, Hive: action.HiveSystem, Subkey: `ControlSet001\Control\MiniNT`, RegType: action.RegSZ},
		// string value
		{Kind: action.AddReg, Hive: action.HiveSystem, Subkey: `Setup`, ValueName: "CmdLine", RegType: action.RegSZ, Data: "cmd.exe /k startnet.cmd", HasValue: true},
		// dword value (hex)
		{Kind: action.AddReg, Hive: action.HiveSystem, Subkey: `Setup`, ValueName: "SetupType", RegType: action.RegDWord, Data: "0x1", HasValue: true},
		// expand-sz in software
		{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: `Microsoft\Windows NT\CurrentVersion\Winlogon`, ValueName: "Shell", RegType: action.RegSZ, Data: "cmd.exe", HasValue: true},
		// empty multi-sz (BootExecute)
		{Kind: action.AddReg, Hive: action.HiveSystem, Subkey: `ControlSet001\Control\Session Manager`, ValueName: "BootExecute", RegType: action.RegMultiSZ, Data: "", HasValue: true},
	}
	if err := h.Apply(acts); err != nil {
		t.Fatal(err)
	}

	if _, ok := h.System.Root.FindPath(`ControlSet001\Control\MiniNT`); !ok {
		t.Error("MiniNT key not created")
	}
	setup, ok := h.System.Root.FindPath("Setup")
	if !ok {
		t.Fatal("Setup key not created")
	}
	if v, ok := setup.Value("CmdLine"); !ok || v.AsString() != "cmd.exe /k startnet.cmd" {
		t.Errorf("Setup\\CmdLine = %q, %v", v.AsString(), ok)
	}
	if v, ok := setup.Value("SetupType"); !ok || v.Type != RegDWord || v.AsDWord() != 1 {
		t.Errorf("Setup\\SetupType = %d (type %d), %v", v.AsDWord(), v.Type, ok)
	}
	wl, ok := h.Software.Root.FindPath(`Microsoft\Windows NT\CurrentVersion\Winlogon`)
	if !ok {
		t.Fatal("Winlogon key not created")
	}
	if v, _ := wl.Value("Shell"); v.AsString() != "cmd.exe" {
		t.Errorf("Shell = %q", v.AsString())
	}
	sm, _ := h.System.Root.FindPath(`ControlSet001\Control\Session Manager`)
	if v, ok := sm.Value("BootExecute"); !ok || v.Type != RegMultiSZ {
		t.Errorf("BootExecute type = %d, %v", v.Type, ok)
	}
}

func TestApplyDelReg(t *testing.T) {
	h := newHives()
	// Seed a value and a subkey, then delete each.
	h.Software.Root.EnsurePath(`Keep\Gone`).SetValue(Value{Name: "v", Type: RegSZ, Data: EncodeSZ("x")})
	h.Software.Root.EnsurePath("Keep").SetValue(Value{Name: "stay", Type: RegSZ, Data: EncodeSZ("y")})

	acts := []action.Action{
		{Kind: action.DelReg, Hive: action.HiveSoftware, Subkey: `Keep\Gone`},               // delete subkey
		{Kind: action.DelReg, Hive: action.HiveSoftware, Subkey: "Keep", ValueName: "stay"}, // delete value
	}
	if err := h.Apply(acts); err != nil {
		t.Fatal(err)
	}
	keep, ok := h.Software.Root.FindPath("Keep")
	if !ok {
		t.Fatal("Keep should still exist")
	}
	if keep.Child("Gone") != nil {
		t.Error("Keep\\Gone should be deleted")
	}
	if _, ok := keep.Value("stay"); ok {
		t.Error("Keep\\stay value should be deleted")
	}
}

func TestApplyLastWriterWins(t *testing.T) {
	h := newHives()
	acts := []action.Action{
		{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: "K", ValueName: "v", RegType: action.RegSZ, Data: "first", HasValue: true},
		{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: "K", ValueName: "v", RegType: action.RegSZ, Data: "second", HasValue: true},
	}
	if err := h.Apply(acts); err != nil {
		t.Fatal(err)
	}
	k, _ := h.Software.Root.FindPath("K")
	if v, _ := k.Value("v"); v.AsString() != "second" {
		t.Errorf("last-writer-wins: got %q, want %q", v.AsString(), "second")
	}
}
