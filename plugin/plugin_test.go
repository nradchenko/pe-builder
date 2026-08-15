// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/internal/golden"
)

// renderPlugin dumps a plugin descriptor and its compiled actions to a stable form.
func renderPlugin(p *Plugin) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:    %s\n", p.Name)
	fmt.Fprintf(&b, "Display: %s\n", p.Display)
	fmt.Fprintf(&b, "Enable:  %v\n", p.DeclaredEnable)
	fmt.Fprintf(&b, "Help:    %s\n", p.Help)
	fmt.Fprintf(&b, "Config:  %s\n", p.Config)
	if p.Update.Present {
		fmt.Fprintf(&b, "Update:  id=%s version=%s url=%s depends=%v parent=%s\n",
			p.Update.ID, p.Update.Version, p.Update.URL, p.Update.Depends, p.Update.Parent)
	}
	fmt.Fprintf(&b, "Actions (%d):\n", len(p.Actions))
	for _, a := range p.Actions {
		fmt.Fprintf(&b, "  %s\n", renderAction(a))
	}
	return b.String()
}

func renderAction(a action.Action) string {
	switch a.Kind {
	case action.Mkdir:
		return fmt.Sprintf("mkdir     id=%s path=%q always=%v fromRoot=%v",
			a.DirID, a.Path, a.AlwaysCreate, a.FromRoot)
	case action.CopyFile:
		return fmt.Sprintf("copy      src=%q dir=%s rename=%q ship=%v noDecomp=%v opt=%v upper=%v",
			a.Src, a.DirID, a.Rename, a.ShipWithPlugin, a.NoDecompress, a.Optional, a.Uppercase)
	case action.AddReg:
		data := a.Data
		if a.DataBytes != nil {
			data = fmt.Sprintf("%x", a.DataBytes)
		}
		return fmt.Sprintf("addreg    hive=%s type=%#x subkey=%q value=%q data=%q",
			a.Hive, a.RegType, a.Subkey, a.ValueName, data)
	case action.DelReg:
		return fmt.Sprintf("delreg    hive=%s subkey=%q value=%q", a.Hive, a.Subkey, a.ValueName)
	case action.SetValue:
		return fmt.Sprintf("setvalue  file=%q section=%q key=%q data=%q",
			a.TargetFile, a.TargetSection, a.LineKey, a.LineData)
	case action.AddLine:
		return fmt.Sprintf("addline   file=%q section=%q line=%q", a.TargetFile, a.TargetSection, a.LineData)
	case action.DelLine:
		return fmt.Sprintf("delline   file=%q section=%q prefix=%q", a.TargetFile, a.TargetSection, a.LineData)
	case action.Append:
		return fmt.Sprintf("append    file=%q from=%q", a.TargetFile, a.AppendFile)
	default:
		return fmt.Sprintf("%s (unrendered)", a.Kind)
	}
}

func parseFixture(t *testing.T, path string, build int) *Plugin {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := inf.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	name := "sample"
	p, err := Parse(name, f, Target{Build: build})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseSamplePluginGolden(t *testing.T) {
	p := parseFixture(t, "testdata/drop/sample/sample.inf", 2600)
	golden.Assert(t, "sample-plugin", renderPlugin(p))
}

func TestEnableDefault(t *testing.T) {
	// No Enable= key: default disabled.
	f, _ := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\""))
	p, err := Parse("x", f, Target{})
	if err != nil {
		t.Fatal(err)
	}
	if p.DeclaredEnable {
		t.Error("DeclaredEnable = true with no Enable= key, want false")
	}
	if p.Display != "X" {
		t.Errorf("Display = %q, want X", p.Display)
	}
}

func TestDoubledQuoteInName(t *testing.T) {
	p := parseFixture(t, "testdata/drop/sample/sample.inf", 2600)
	if p.Display != `Sample Plugin ("demo")` {
		t.Errorf("Display = %q", p.Display)
	}
}

func TestSignatureCaseInsensitive(t *testing.T) {
	// setupapi matches the [Version] signature case-insensitively.
	f, err := inf.Parse([]byte("[Version]\nSignature=\"$WINDOWS NT$\"\n[PEBuilder]\nName=\"X\""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("x", f, Target{}); err != nil {
		t.Errorf("case-variant signature rejected: %v", err)
	}
}

func TestMultiSZCapturesAllStrings(t *testing.T) {
	f, err := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n" +
		"[Software.AddReg]\n0x7,\"Sub\",\"Name\",\"a\",\"b\",\"c\""))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("x", f, Target{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range p.Actions {
		if a.Kind == action.AddReg && a.RegType == action.RegMultiSZ {
			found = true
			if a.Data != "a\x00b\x00c" {
				t.Errorf("MULTI_SZ Data = %q, want the three NUL-joined strings", a.Data)
			}
		}
	}
	if !found {
		t.Error("no REG_MULTI_SZ action produced")
	}
}

func TestMissingRequiredSections(t *testing.T) {
	cases := map[string]string{
		"no version":    "[PEBuilder]\nName=\"X\"",
		"bad signature": "[Version]\nSignature=\"nope\"\n[PEBuilder]\nName=\"X\"",
		"no pebuilder":  "[Version]\nSignature=\"$Windows NT$\"",
	}
	for name, text := range cases {
		f, err := inf.Parse([]byte(text))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if _, err := Parse("x", f, Target{}); err == nil {
			t.Errorf("%s: expected error, got none", name)
		}
	}
}

// TestArchAffectsActions covers the architecture-conditional sections: an INF adds
// arch-specific entries in a .<arch> section and the shared ones stay in the
// unsuffixed section, so each architecture sees the shared set plus its own. It is
// the same compose rule .BuildNo uses, on the other axis.
func TestArchAffectsActions(t *testing.T) {
	const src = `[Version]
Signature="$Windows NT$"
[PEBuilder]
Name="X"
[SourceDisksFiles]
shared.dll =
[SourceDisksFiles.i386]
x86only.dll =
[SourceDisksFiles.amd64]
x64only.dll =
[SetValue]
"txtsetup.sif","SetupData","loaderprompt","""shared"""
[SetValue.amd64]
"txtsetup.sif","SetupData","osloadoptions","""/hal=hal.dll"""
`
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	copies := func(arch string) []string {
		acts, err := Actions(f, Target{Arch: arch})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range acts {
			switch a.Kind {
			case action.CopyFile:
				out = append(out, a.Src)
			case action.SetValue:
				out = append(out, a.LineKey)
			}
		}
		return out
	}

	// The shared entries appear for every architecture and each arch's own appear only
	// for it. An unset architecture resolves to the x86 layout — the same default
	// source.Identity.Roots applies to a source's folders, so a zero identity cannot
	// get x86 folders while silently compiling neither architecture's sections.
	for _, tt := range []struct {
		arch string
		want []string
	}{
		{"i386", []string{"shared.dll", "x86only.dll", "loaderprompt"}},
		{"amd64", []string{"shared.dll", "x64only.dll", "loaderprompt", "osloadoptions"}},
		{"", []string{"shared.dll", "x86only.dll", "loaderprompt"}},
	} {
		if diff := cmp.Diff(tt.want, copies(tt.arch)); diff != "" {
			t.Errorf("arch %q (-want +got):\n%s", tt.arch, diff)
		}
	}
}

// TestUnknownArchRejected covers an architecture that matches no section. Left
// unchecked it would compile the architecture-neutral content alone and report
// success, which for the base layer means dropping the options that make the output a
// PE — a silent wrong answer rather than a failure.
func TestUnknownArchRejected(t *testing.T) {
	f, err := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"AMD64", "x86", "x64", "ia64"} {
		if _, err := Actions(f, Target{Arch: arch}); err == nil {
			t.Errorf("Actions accepted unknown architecture %q", arch)
		}
	}
	// The two a source can actually have, and the unset default, are accepted.
	for _, arch := range []string{"i386", "amd64", ""} {
		if _, err := Actions(f, Target{Arch: arch}); err != nil {
			t.Errorf("Actions rejected valid architecture %q: %v", arch, err)
		}
	}
}

func TestBuildNoAffectsActions(t *testing.T) {
	// The .2600 copy (xp-widget.sys) appears only at build 2600.
	has := func(build int) bool {
		p := parseFixture(t, "testdata/drop/sample/sample.inf", build)
		for _, a := range p.Actions {
			if a.Kind == action.CopyFile && a.Src == "xp-widget.sys" {
				return true
			}
		}
		return false
	}
	if !has(2600) {
		t.Error("xp-widget.sys missing at build 2600")
	}
	if has(3790) {
		t.Error("xp-widget.sys present at build 3790")
	}
}
