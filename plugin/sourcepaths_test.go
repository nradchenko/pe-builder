// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"testing"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// TestSourcePathsActions checks that [SourcePaths] compiles each entry to a copy that
// reads an exact source path and writes a literal output-tree path (backslashes
// normalized), for files a basename lookup could not resolve.
func TestSourcePathsActions(t *testing.T) {
	src := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"P\"\nEnable=1\n" +
		"[SourcePaths]\n" +
		`ASMS\6000\MSFT\WINDOWS\COMMON\CONTROLS\COMCTL32.DLL = i386\WinSxS\asm_6.0.0.0\comctl32.dll` + "\n"
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	acts, err := Actions(f, 0)
	if err != nil {
		t.Fatal(err)
	}

	var got []action.Action
	for _, a := range acts {
		if a.SrcPath != "" {
			got = append(got, a)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d [SourcePaths] copies, want 1: %+v", len(got), acts)
	}
	a := got[0]
	if a.Kind != action.CopyFile {
		t.Errorf("kind = %v, want CopyFile", a.Kind)
	}
	if want := "ASMS/6000/MSFT/WINDOWS/COMMON/CONTROLS/COMCTL32.DLL"; a.SrcPath != want {
		t.Errorf("SrcPath = %q, want %q", a.SrcPath, want)
	}
	if want := "i386/WinSxS/asm_6.0.0.0/comctl32.dll"; a.Dest != want {
		t.Errorf("Dest = %q, want %q", a.Dest, want)
	}
}

// TestSourcePathsSkipsIncomplete checks a line missing either side is ignored, not
// turned into a broken copy.
func TestSourcePathsSkipsIncomplete(t *testing.T) {
	src := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"P\"\nEnable=1\n" +
		"[SourcePaths]\n" +
		`ASMS\ONLY\SOURCE.DLL =` + "\n"
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	acts, err := Actions(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.SrcPath != "" {
			t.Errorf("incomplete [SourcePaths] line produced a copy: %+v", a)
		}
	}
}
