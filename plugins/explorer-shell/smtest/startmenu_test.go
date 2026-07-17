// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package smtest holds the host-side golden test for the Start-menu planner. It lives in its
// own directory (not beside the plugin's .c files, which `go test` would reject in a Go
// package) and exists only so `go test` can exercise the pure C planner (startmenu.c).
package smtest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/internal/golden"
)

// TestStartMenuPlanner compiles the pure planner (startmenu.c) with the host C compiler via
// its test harness (startmenu_testmain.c), feeds each fixture `pe-builder\StartMenu` contract
// on stdin, and golden-compares the folder/shortcut layout it emits. This is the layout
// contract the on-target generator honors; the Windows-only halves (FileDescription lookup,
// .lnk creation) are stubbed. Skips where no host C compiler is available.
func TestStartMenuPlanner(t *testing.T) {
	cc := findCC(t)
	bin := filepath.Join(t.TempDir(), "smtest")
	build := exec.Command(cc, "-O0", "-Wall", "-Wextra", "-o", bin, "../startmenu_testmain.c")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile planner harness: %v\n%s", err, out)
	}

	cases := []struct {
		name    string
		fixture string
	}{
		// Bare names stay at the All Programs top level (no MKDIR). The label comes from the
		// target's FileDescription when it has one (regedit, localized here), and falls back to
		// the value-name leaf when it does not (cmd) — so existing bare-name entries keep working.
		{"flat", `
FD	%SystemRoot%\regedit.exe	Редактор реестра
E	Registry Editor	%SystemRoot%\regedit.exe
E	Command Prompt	%SystemRoot%\system32\cmd.exe
`},
		// A backslash in the value name encodes a submenu path; intermediate folders are ensured
		// parent-first, and the shortcut lands in the deepest folder.
		{"submenu", `
FD	%SystemRoot%\system32\sol.exe	Solitaire
E	Games\Solitaire	%SystemRoot%\system32\sol.exe
E	Games\Minesweeper	%SystemRoot%\system32\winmine.exe
E	Games\Board\Chess	%SystemRoot%\system32\chess.exe
`},
		// An explicit "target|Label" overrides both the FileDescription and the leaf; surrounding
		// spaces around the two halves are trimmed.
		{"override", `
FD	%SystemRoot%\system32\cmd.exe	Windows Command Processor
E	Command Prompt	%SystemRoot%\system32\cmd.exe|Command Prompt
E	Tools\Shell	%SystemRoot%\system32\cmd.exe | My Shell
`},
		// A single leading backslash anchors the entry at the Start Menu root (top-level classic
		// menu) instead of Programs — shown by the "root" anchor column; a submenu can hang off
		// the root too. A bare name still resolves under "prog" (All Programs), with its localized
		// FileDescription label.
		{"rootlevel", `
FD	%SystemRoot%\system32\calc.exe	Калькулятор
E	\Command Prompt	%SystemRoot%\system32\cmd.exe
E	\Games\Chess	%SystemRoot%\system32\chess.exe
E	Calculator	%SystemRoot%\system32\calc.exe
`},
		// Path-illegal characters are stripped from the on-disk name; entries that yield no leaf
		// (a trailing or lone separator) are skipped; an empty middle component collapses away.
		{"edge", `
E	Bad:Name	%SystemRoot%\foo.exe
E	Games\	%SystemRoot%\foo.exe
E	\	%SystemRoot%\foo.exe
E	A\\B	%SystemRoot%\foo.exe
`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Stdin = strings.NewReader(strings.TrimPrefix(c.fixture, "\n"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run harness: %v\n%s", err, out)
			}
			golden.Assert(t, "startmenu/"+c.name, string(out))
		})
	}
}

func findCC(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no host C compiler (cc/gcc/clang) on PATH")
	return ""
}
