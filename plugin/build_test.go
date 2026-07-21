// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"bytes"
	"context"
	"encoding/binary"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/source"
)

func TestBuiltinPoweroff(t *testing.T) {
	// poweroff is a built-in, build-capable plugin.
	names, err := Builtin().List()
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(names, "poweroff") {
		t.Fatalf("built-in plugins = %v, want poweroff among them", names)
	}
	fsys, canonical, ok := Builtin().Resolve("poweroff")
	if !ok {
		t.Fatal("Resolve(poweroff) not found in the built-in source")
	}
	p, err := ParseFS(fsys, canonical, Target{Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Build.Present {
		t.Fatal("poweroff has no [Build] section")
	}
	if !containsStr(p.Build.Requires, "i686-w64-mingw32-gcc") || !containsStr(p.Build.Produces, "poweroff.exe") {
		t.Errorf("poweroff [Build] = %+v, want mingw Requires and poweroff.exe Produces", p.Build)
	}

	// It ships source, not a binary.
	if _, err := fs.Stat(fsys, "poweroff.exe"); err == nil {
		t.Error("the built-in poweroff embeds a prebuilt poweroff.exe; it must ship source only")
	}
	if _, err := fs.Stat(fsys, "poweroff.c"); err != nil {
		t.Errorf("poweroff.c not embedded: %v", err)
	}

	// Build it for real when the mingw toolchain is available.
	if _, err := exec.LookPath("i686-w64-mingw32-gcc"); err != nil {
		t.Skip("mingw i686 toolchain not present; skipping the compile")
	}
	d := &Discovered{Name: canonical, Source: BuiltinLabel, FS: fsys, Units: []*Plugin{p}}
	built, err := RunBuild(context.Background(), d, t.TempDir(), "i386")
	if err != nil {
		t.Fatalf("poweroff build failed: %v", err)
	}
	if !built.Produces["poweroff.exe"] {
		t.Fatal("build did not produce poweroff.exe")
	}
	bin, err := fs.ReadFile(built.Dir, "poweroff.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bin, []byte("MZ")) {
		t.Error("produced poweroff.exe is not a PE/DOS executable (no MZ header)")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

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
	p, err := Parse("x", f, Target{})
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

// TestParseBuildArch covers the architecture fold: [Build.<arch>] adds to [Build]
// for the matching architecture, so the shared Produces stays in [Build] while each
// architecture names its own toolchain in Requires and hands the arch to the build
// through Command. It is the compose rule the action sections use, on [Build].
func TestParseBuildArch(t *testing.T) {
	const src = `[Version]
Signature="$Windows NT$"
[PEBuilder]
Name="X"
[Build]
Produces = drv.sys
[Build.i386]
Requires = i686-w64-mingw32-gcc make
Command  = make ARCH=i386
[Build.amd64]
Requires = x86_64-w64-mingw32-gcc make
Command  = make ARCH=amd64
`
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		arch, wantReq, wantCmd string
	}{
		// The arch section's toolchain and Command apply; the shared Produces is seen
		// on every architecture.
		{"i386", "i686-w64-mingw32-gcc,make", "make ARCH=i386"},
		{"amd64", "x86_64-w64-mingw32-gcc,make", "make ARCH=amd64"},
		// An unset architecture resolves to i386, matching how folders and action
		// sections resolve it — a plugin never compiles with neither arch's toolchain.
		{"", "i686-w64-mingw32-gcc,make", "make ARCH=i386"},
	} {
		p, err := Parse("x", f, Target{Arch: tt.arch})
		if err != nil {
			t.Fatalf("arch %q: %v", tt.arch, err)
		}
		if !containsStr(p.Build.Produces, "drv.sys") {
			t.Errorf("arch %q: Produces = %v, want the shared drv.sys", tt.arch, p.Build.Produces)
		}
		if got := strings.Join(p.Build.Requires, ","); got != tt.wantReq {
			t.Errorf("arch %q: Requires = %q, want %q", tt.arch, got, tt.wantReq)
		}
		if p.Build.Command != tt.wantCmd {
			t.Errorf("arch %q: Command = %q, want %q", tt.arch, p.Build.Command, tt.wantCmd)
		}
	}
}

// TestParseBuildVariantWithoutBase covers a [Build.<arch>] written without the [Build]
// it varies. Read as "not build-capable" it would be silent: the binary never compiled,
// and the first symptom a missing-file error from the tree assembler, nowhere near the
// cause. A build-number variant is the same mistake and is caught the same way.
func TestParseBuildVariantWithoutBase(t *testing.T) {
	const head = "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n"
	for _, body := range []string{
		"[Build.amd64]\nRequires = x86_64-w64-mingw32-gcc\nProduces = drv.sys\n",
		"[Build.i386]\nProduces = drv.sys\n",
		"[Build.2600]\nProduces = drv.sys\n",
	} {
		f, err := inf.Parse([]byte(head + body))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse("x", f, Target{Arch: "amd64"}); err == nil {
			t.Errorf("a variant without a base [Build] was accepted:\n%s", body)
		}
	}
	// A base [Build] alone, and a base with variants, both stay valid.
	for _, body := range []string{
		"[Build]\nProduces = drv.sys\n",
		"[Build]\nProduces = drv.sys\n[Build.amd64]\nCommand = make ARCH=amd64\n",
	} {
		f, _ := inf.Parse([]byte(head + body))
		if _, err := Parse("x", f, Target{Arch: "amd64"}); err != nil {
			t.Errorf("valid [Build] rejected: %v\n%s", err, body)
		}
	}
}

func TestParseBuildCommandVerbatim(t *testing.T) {
	// The command is taken verbatim: quotes and commas survive (they would be
	// stripped/truncated by INF field splitting).
	f, _ := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n" +
		"[Build]\nCommand = gcc -DV=\"1,2\" -o t.exe t.c\n"))
	p, err := Parse("x", f, Target{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Build.Command != `gcc -DV="1,2" -o t.exe t.c` {
		t.Errorf("Command = %q, want the verbatim command with quotes and comma intact", p.Build.Command)
	}
}

func TestParseBuildDefaultCommand(t *testing.T) {
	f, _ := inf.Parse([]byte("[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n[Build]\nProduces=a.exe\n"))
	p, err := Parse("x", f, Target{})
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
	return buildPluginWithFile(t, "", nil, spec)
}

// buildPluginWithFile is buildPlugin with an extra file shipped in the plugin's directory,
// for a build command that needs an input it did not compile itself.
func buildPluginWithFile(t *testing.T, name string, data []byte, spec BuildSpec) *Discovered {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "source.c"), []byte("int main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Discovered{
		Name:  "demo",
		FS:    os.DirFS(srcDir),
		Units: []*Plugin{{Name: "demo", Build: spec}},
	}
}

// TestParseBuildMarksMissingArchVariant checks which plugins are flagged as unbuildable for
// the target architecture — the flag RunBuild refuses on (TestRunBuildRejectsMissingArchVariant
// covers the refusal itself). Parsing must stay tolerant: every case here has to PARSE, since
// a parse error would drop the plugin from discovery entirely.
func TestParseBuildMarksMissingArchVariant(t *testing.T) {
	const head = "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n"
	const onlyI386 = "[Build]\nProduces = drv.sys\n[Build.i386]\nRequires = i686-w64-mingw32-gcc\nCommand = make ARCH=i386\n"

	for _, tt := range []struct {
		name, body, arch, want string
	}{
		{"scoped to i386, built for amd64", onlyI386, "amd64", "amd64"},
		{"scoped to i386, built for i386", onlyI386, "i386", ""},
		// An unscoped [Build] applies to every architecture and is never flagged.
		{"unscoped", "[Build]\nRequires = make\nProduces = drv.sys\n", "amd64", ""},
		// A build-number variant is not architecture scoping and must not trigger the rule.
		{"build-number variant only", "[Build]\nProduces = drv.sys\n[Build.2600]\nCommand = make x\n", "amd64", ""},
		// An unset architecture resolves to i386, matching folders and action sections.
		{"unset arch resolves to i386", onlyI386, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parseTargetArch(t, head+tt.body, tt.arch)
			if err != nil {
				t.Fatalf("must still parse (a parse error drops the plugin from discovery): %v", err)
			}
			if p.Build.MissingArchVariant != tt.want {
				t.Errorf("MissingArchVariant = %q, want %q", p.Build.MissingArchVariant, tt.want)
			}
		})
	}
}

func parseTargetArch(t *testing.T, src, arch string) (*Plugin, error) {
	t.Helper()
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return Parse("x", f, Target{Arch: arch})
}

// fakePE returns the bytes of a file that is a valid enough PE for a machine-type check:
// the MZ stamp, an e_lfanew pointing at the PE signature, and the COFF Machine field.
func fakePE(machine uint16) []byte {
	b := make([]byte, 0x50)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], []byte{'P', 'E', 0, 0})
	binary.LittleEndian.PutUint16(b[0x44:], machine)
	return b
}

// TestMachineForCoversEveryArch pins the two architecture lists together. checkMachine
// returns nil for any architecture missing from machineFor, so adding one to
// source.archFolders — the natural single place to add an architecture, and what decides
// which [Build.<arch>] sections are recognised — would silently disable the
// wrong-architecture guard for exactly the newest and least-tested target. Nothing else
// connects the two: they live in different packages and neither fails to compile.
func TestMachineForCoversEveryArch(t *testing.T) {
	for _, folder := range source.ArchFolders() {
		arch := folder // the folder name is the arch name for every architecture so far
		if _, ok := machineFor[arch]; !ok {
			t.Errorf("machineFor has no entry for architecture %q, so a build for it would "+
				"skip the wrong-architecture check entirely", arch)
		}
	}
	// A literal expectation too, so an emptied ArchFolders() cannot make the loop above
	// vacuous and leave this test green while checking nothing.
	if machineFor["amd64"] != 0x8664 || machineFor["i386"] != 0x014c {
		t.Errorf("machineFor = %v, want i386 0x014c and amd64 0x8664", machineFor)
	}
}

// TestRunBuildRejectsMissingArchVariant checks that a plugin scoping its build by
// architecture without a variant for the target is refused when BUILT, and — the point of
// the altitude — still parses, so it stays discoverable and describable. Refusing it at
// parse time would drop it from the search path entirely (Discover skips a manifest that
// fails to parse), so a manifest naming it would report "plugin not found" rather than the
// real reason, and `plugins list` would omit it.
func TestRunBuildRejectsMissingArchVariant(t *testing.T) {
	const src = "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"X\"\n" +
		"[Build]\nProduces = drv.sys\n[Build.i386]\nRequires = i686-w64-mingw32-gcc\nCommand = make ARCH=i386\n"
	f, err := inf.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse("demo", f, Target{Arch: "amd64"})
	if err != nil {
		t.Fatalf("an i386-only plugin must still PARSE for an amd64 target: %v", err)
	}
	if p.Build.MissingArchVariant != "amd64" {
		t.Errorf("MissingArchVariant = %q, want amd64", p.Build.MissingArchVariant)
	}

	d := &Discovered{Name: "demo", FS: os.DirFS(t.TempDir()), Units: []*Plugin{p}}
	_, err = RunBuild(context.Background(), d, t.TempDir(), "amd64")
	if err == nil {
		t.Fatal("building a plugin with no [Build.amd64] for an amd64 target was accepted")
	}
	if !strings.Contains(err.Error(), "amd64") || !strings.Contains(err.Error(), "demo") {
		t.Errorf("error %q should name both the plugin and the architecture", err)
	}

	// The architecture it does declare builds normally.
	pi386, err := Parse("demo", f, Target{Arch: "i386"})
	if err != nil {
		t.Fatal(err)
	}
	if pi386.Build.MissingArchVariant != "" {
		t.Errorf("MissingArchVariant = %q for the declared architecture, want empty",
			pi386.Build.MissingArchVariant)
	}
}

// TestRunBuildRejectsWrongArchOutput checks that a build whose output is for the wrong
// architecture fails loudly. Nothing else catches it: make decides by timestamp and no
// plugin Makefile makes its outputs depend on ARCH, so building for one architecture and
// then another in the same directory leaves the first one's binary in place. The staged
// binary would simply not run, with nothing in the build output explaining why.
func TestRunBuildRejectsWrongArchOutput(t *testing.T) {
	for _, tt := range []struct {
		name, arch string
		machine    uint16
		wantErr    bool
	}{
		{"amd64 output for an i386 build", "i386", 0x8664, true},
		{"i386 output for an amd64 build", "amd64", 0x014c, true},
		{"i386 output for an i386 build", "i386", 0x014c, false},
		{"amd64 output for an amd64 build", "amd64", 0x8664, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The image is shipped in the plugin's own directory, which RunBuild
			// materializes into the build dir, so the command is a plain copy and the
			// test depends on nothing beyond sh.
			d := buildPluginWithFile(t, "image.bin", fakePE(tt.machine), BuildSpec{
				Present:  true,
				Requires: []string{"sh"},
				Produces: []string{"out.exe"},
				Command:  "cp image.bin out.exe",
			})
			_, err := RunBuild(context.Background(), d, t.TempDir(), tt.arch)
			if tt.wantErr && err == nil {
				t.Errorf("a %#04x image was accepted for a %s build", tt.machine, tt.arch)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("a matching image was rejected: %v", err)
			}
		})
	}
}

// A produced file that is not a PE at all has no machine type and must pass unremarked.
func TestRunBuildIgnoresNonPEOutput(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Requires: []string{"sh"},
		Produces: []string{"out.txt"},
		Command:  "printf 'just text' > out.txt",
	})
	if _, err := RunBuild(context.Background(), d, t.TempDir(), "amd64"); err != nil {
		t.Errorf("a non-PE output was rejected: %v", err)
	}
}

func TestRunBuildSuccess(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Requires: []string{"sh"},
		Produces: []string{"out.txt"},
		Command:  "printf built > out.txt",
	})

	built, err := RunBuild(context.Background(), d, t.TempDir(), "i386")
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
	if _, err := RunBuild(context.Background(), d, t.TempDir(), "i386"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "out.txt")); err == nil {
		t.Error("RunBuild wrote into the plugin's own source directory")
	}
}

func TestRunBuildNoSection(t *testing.T) {
	d := &Discovered{Name: "plain", Units: []*Plugin{{Name: "plain"}}}
	built, err := RunBuild(context.Background(), d, t.TempDir(), "i386")
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
	_, err := RunBuild(context.Background(), d, t.TempDir(), "i386")
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
	if _, err := RunBuild(context.Background(), d, t.TempDir(), "i386"); err != nil {
		t.Errorf("case-mismatched Produces failed: %v", err)
	}
}

func TestRunBuildNilFS(t *testing.T) {
	// A build-capable plugin with no source directory is a clean error, not a panic.
	d := &Discovered{Name: "x", Units: []*Plugin{{Name: "x", Build: BuildSpec{Present: true, Command: "true"}}}}
	if _, err := RunBuild(context.Background(), d, t.TempDir(), "i386"); err == nil {
		t.Error("RunBuild with a nil FS returned no error")
	}
}

func TestRunBuildMissingProduces(t *testing.T) {
	d := buildPlugin(t, BuildSpec{
		Present:  true,
		Produces: []string{"never-made.exe"},
		Command:  "true",
	})
	_, err := RunBuild(context.Background(), d, t.TempDir(), "i386")
	if err == nil {
		t.Fatal("RunBuild that did not produce its file returned no error")
	}
	if !strings.Contains(err.Error(), "never-made.exe") {
		t.Errorf("error %q should name the missing produced file", err)
	}
}
