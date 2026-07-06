// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBaseHivesFromSource(t *testing.T) {
	b := sourceBacking(t) // skips when the ISO is absent
	hives, err := BaseHives(b)
	if err != nil {
		t.Fatalf("BaseHives: %v", err)
	}

	// SYSTEM comes from the pre-built setupreg.hiv.
	if hives.System.Root.Child("ControlSet001") == nil {
		t.Error("SYSTEM missing ControlSet001")
	}

	// SOFTWARE from hivesft.inf, with the SOFTWARE prefix mapped to the root.
	winNT := hives.Software.Root.EnsurePath(`Microsoft\Windows NT\CurrentVersion`)
	if winNT == nil || len(hives.Software.Root.Subkeys) == 0 {
		t.Error("SOFTWARE missing Microsoft\\Windows NT\\CurrentVersion")
	}
	if hives.Software.Root.Child("SOFTWARE") != nil {
		t.Error("SOFTWARE prefix was not stripped when building the SOFTWARE hive")
	}
	if hives.Software.Root.EnsurePath(`Microsoft\Windows\CurrentVersion`) == nil {
		t.Error("SOFTWARE missing Microsoft\\Windows\\CurrentVersion")
	}

	// DEFAULT from hivedef.inf (HKCU maps directly to the root).
	for _, name := range []string{"AppEvents", "Control Panel", "Software"} {
		if hives.Default.Root.Child(name) == nil {
			t.Errorf("DEFAULT missing %q", name)
		}
	}

	// CLASSES from hivecls.inf (HKCR maps directly to the root).
	if hives.Classes.Root.Child("*") == nil {
		t.Error("CLASSES missing the '*' key")
	}
	if hives.Classes.Root.Child("CLSID") == nil {
		t.Error("CLASSES missing CLSID")
	}

	// Every base hive must serialize and pass independent validation.
	hivexml, _ := exec.LookPath("hivexml")
	for _, h := range []struct {
		name string
		hive *Hive
	}{
		{"system", hives.System},
		{"software", hives.Software},
		{"default", hives.Default},
		{"classes", hives.Classes},
	} {
		data, err := WriteHive(h.hive)
		if err != nil {
			t.Errorf("WriteHive(%s): %v", h.name, err)
			continue
		}
		if hivexml == "" {
			continue
		}
		path := filepath.Join(t.TempDir(), h.name+".hiv")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(hivexml, path).CombinedOutput(); err != nil {
			t.Errorf("hivexml rejected the %s hive: %v\n%s", h.name, err, out)
		}
	}
}

// TestSoftwareValueTypesFromSource spot-checks that real hivesft.inf lines decode
// to the right value types: a plain REG_SZ string and a setupapi 0x00010001
// REG_DWORD.
func TestSoftwareValueTypesFromSource(t *testing.T) {
	b := sourceBacking(t)
	hives, err := BaseHives(b)
	if err != nil {
		t.Fatalf("BaseHives: %v", err)
	}
	cv := hives.Software.Root.EnsurePath(`Microsoft\Windows NT\CurrentVersion`)

	pn, ok := cv.Value("ProductName")
	if !ok {
		t.Fatal("SOFTWARE ...\\CurrentVersion has no ProductName value")
	}
	if pn.Type != RegSZ || pn.AsString() != "Microsoft Windows XP" {
		t.Errorf("ProductName = %q (type %d), want \"Microsoft Windows XP\" REG_SZ", pn.AsString(), pn.Type)
	}

	// InstallDate is written with setupapi flags 0x00010003 (REG_DWORD).
	id, ok := cv.Value("InstallDate")
	if !ok {
		t.Fatal("SOFTWARE ...\\CurrentVersion has no InstallDate value")
	}
	if id.Type != RegDWord || len(id.Data) != 4 {
		t.Errorf("InstallDate type = %d, len = %d, want REG_DWORD with 4 bytes", id.Type, len(id.Data))
	}
}
