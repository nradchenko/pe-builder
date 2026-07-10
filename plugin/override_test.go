// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nradchenko/pe-builder/action"
)

func TestParseRegPath(t *testing.T) {
	cases := []struct {
		path   string
		hive   action.RegHive
		subkey string
		value  string
	}{
		{`HKLM\SYSTEM\Setup\CmdLine`, action.HiveSystem, "Setup", "CmdLine"},
		{`HKLM\SOFTWARE\Foo\Bar\Baz`, action.HiveSoftware, `Foo\Bar`, "Baz"},
		{`HKU\.DEFAULT\Control Panel\X`, action.HiveDefault, "Control Panel", "X"},
		{`HKEY_USERS\.default\Env\Y`, action.HiveDefault, "Env", "Y"}, // .default case-folded
		{`HKCU\Env\Path`, action.HiveDefault, "Env", "Path"},
		{`HKLM\SOFTWARE\RootValue`, action.HiveSoftware, "", "RootValue"},
	}
	for _, c := range cases {
		hive, subkey, value, err := parseRegPath(c.path)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.path, err)
			continue
		}
		if hive != c.hive || subkey != c.subkey || value != c.value {
			t.Errorf("%s → (%s, %q, %q), want (%s, %q, %q)",
				c.path, hive, subkey, value, c.hive, c.subkey, c.value)
		}
	}
}

func TestParseRegPathErrors(t *testing.T) {
	for _, p := range []string{
		`HKLM\BOGUS\x`,   // unsupported HKLM root
		`HKCR\Classes\x`, // unrecognized hive
		`HKLM`,           // hive prefix only
		`HKLM\SOFTWARE`,  // names a hive but no value
		`HKU\Someone\x`,  // only HKU\.DEFAULT supported
		``,               // empty
	} {
		if _, _, _, err := parseRegPath(p); err == nil {
			t.Errorf("parseRegPath(%q) returned no error", p)
		}
	}
}

func TestIncludeAction(t *testing.T) {
	a := includeAction("i386/system32/extra.dll")
	if a.Kind != action.CopyFile || a.Src != "extra.dll" || a.Dest != "i386/system32/extra.dll" || a.Origin != "" {
		t.Errorf("includeAction = %+v, want a source copy of extra.dll to i386/system32/extra.dll", a)
	}
}

func TestInjectAction(t *testing.T) {
	local := t.TempDir()
	from := filepath.Join(local, "startnet.cmd")
	if err := os.WriteFile(from, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, dir, err := injectAction(Inject{From: from, To: "i386/system32/startnet.cmd"}, "inject:p:0")
	if err != nil {
		t.Fatal(err)
	}
	if a.Src != "startnet.cmd" || a.Dest != "i386/system32/startnet.cmd" || a.Origin != "inject:p:0" {
		t.Errorf("injectAction = %+v, want a copy of startnet.cmd to the target with the inject origin", a)
	}
	if dir != local {
		t.Errorf("inject dir = %q, want %q", dir, local)
	}

	// A missing inject source is a clear error naming the path.
	if _, _, err := injectAction(Inject{From: filepath.Join(local, "nope.cmd"), To: "i386/x"}, "inject:p:1"); err == nil {
		t.Error("injectAction on a missing source returned no error")
	}
}
