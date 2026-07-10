// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/inf"
)

func TestParseBuildSection(t *testing.T) {
	src := `[Version]
Signature="$Windows NT$"
[PEBuilder]
Name="X"
[Build]
Requires = gcc make
Produces = tool.exe, extra.dll
Command  = make all
`
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("x", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := p.Build
	if !b.Present {
		t.Fatal("Build.Present = false, want true")
	}
	if strings.Join(b.Requires, ",") != "gcc,make" {
		t.Errorf("Requires = %v, want [gcc make]", b.Requires)
	}
	if strings.Join(b.Produces, ",") != "tool.exe,extra.dll" {
		t.Errorf("Produces = %v, want [tool.exe extra.dll]", b.Produces)
	}
	if b.Command != "make all" {
		t.Errorf("Command = %q, want %q", b.Command, "make all")
	}
}

func TestParseBuildCommandVerbatim(t *testing.T) {
	// The command is taken verbatim: quotes and commas survive (they would be
	// stripped/truncated by INF field splitting).
	f, _ := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n" +
		"[Build]\nCommand = gcc -DV=\"1,2\" -o t.exe t.c\n"))
	p, err := Parse("x", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Build.Command != `gcc -DV="1,2" -o t.exe t.c` {
		t.Errorf("Command = %q, want the verbatim command with quotes and comma intact", p.Build.Command)
	}
}

func TestParseBuildDefaultCommand(t *testing.T) {
	f, _ := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n[Build]\nProduces=a.exe\n"))
	p, err := Parse("x", f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Build.Command != "make" {
		t.Errorf("default Command = %q, want make", p.Build.Command)
	}
}

// buildPlugin makes a Discovered whose source directory holds source.c and which
// declares the given [Build] spec.
func buildPlugin(t *testing.T, spec BuildSpec) *Discovered {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "source.c"), []byte("int main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Discovered{
		Name:  "demo",
		FS:    os.DirFS(srcDir),
		Units: []*Plugin{{Name: "demo", Build: spec}},
	}
}

func TestRunBuildSuccess(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Requires: []string{"sh"},
		Produces: []string{"out.txt"},
		Command:  "printf built > out.txt",
	})

	built, err := RunBuild(context.Background(), d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if built == nil {
		t.Fatal("RunBuild returned nil for a build-capable plugin")
	}
	if !built.Produces["out.txt"] {
		t.Errorf("Produces = %v, want out.txt", built.Produces)
	}
	got, err := fs.ReadFile(built.Dir, "out.txt")
	if err != nil {
		t.Fatalf("read produced file: %v", err)
	}
	if string(got) != "built" {
		t.Errorf("produced file = %q, want built", got)
	}
}

func TestRunBuildDoesNotTouchSource(t *testing.T) {
	// The build runs in a materialized copy, so the plugin's own directory is untouched.
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "source.c"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Discovered{
		Name: "demo",
		FS:   os.DirFS(srcDir),
		Units: []*Plugin{{Name: "demo", Build: BuildSpec{
			Present: true, Produces: []string{"out.txt"}, Command: "printf x > out.txt",
		}}},
	}
	if _, err := RunBuild(context.Background(), d, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "out.txt")); err == nil {
		t.Error("RunBuild wrote into the plugin's own source directory")
	}
}

func TestRunBuildNoSection(t *testing.T) {
	d := &Discovered{Name: "plain", Units: []*Plugin{{Name: "plain"}}}
	built, err := RunBuild(context.Background(), d, t.TempDir())
	if err != nil || built != nil {
		t.Fatalf("RunBuild on a [Build]-less plugin = (%v, %v), want (nil, nil)", built, err)
	}
}

func TestRunBuildMissingTool(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Requires: []string{"definitely-not-a-real-tool-xyzzy"},
		Produces: []string{"out.txt"},
		Command:  "printf x > out.txt",
	})
	_, err := RunBuild(context.Background(), d, t.TempDir())
	if err == nil {
		t.Fatal("RunBuild with a missing required tool returned no error")
	}
	if !strings.Contains(err.Error(), "demo") || !strings.Contains(err.Error(), "xyzzy") {
		t.Errorf("error %q should name the plugin and the missing tool", err)
	}
}

func TestRunBuildProducesCaseTolerant(t *testing.T) {
	// The plugin declares OUT.TXT but the command emits out.txt; the existence check
	// tolerates the case difference, matching how the tree reads the origin.
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Produces: []string{"OUT.TXT"},
		Command:  "printf x > out.txt",
	})
	if _, err := RunBuild(context.Background(), d, t.TempDir()); err != nil {
		t.Errorf("case-mismatched Produces failed: %v", err)
	}
}

func TestRunBuildNilFS(t *testing.T) {
	// A build-capable plugin with no source directory is a clean error, not a panic.
	d := &Discovered{Name: "x", Units: []*Plugin{{Name: "x", Build: BuildSpec{Present: true, Command: "true"}}}}
	if _, err := RunBuild(context.Background(), d, t.TempDir()); err == nil {
		t.Error("RunBuild with a nil FS returned no error")
	}
}

func TestRunBuildMissingProduces(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Produces: []string{"never-made.exe"},
		Command:  "true",
	})
	_, err := RunBuild(context.Background(), d, t.TempDir())
	if err == nil {
		t.Fatal("RunBuild that did not produce its file returned no error")
	}
	if !strings.Contains(err.Error(), "never-made.exe") {
		t.Errorf("error %q should name the missing produced file", err)
	}
}
