// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/base"
	"github.com/nradchenko/pe-builder/boot"
	"github.com/nradchenko/pe-builder/hive"
	"github.com/nradchenko/pe-builder/internal/enginehash"
	"github.com/nradchenko/pe-builder/internal/golden"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/plugin"
	"github.com/nradchenko/pe-builder/plugins"
	"github.com/nradchenko/pe-builder/receipt"
	"github.com/nradchenko/pe-builder/source"
)

// minimalHiveINF is a valid, empty hive-population INF: setupapi accepts it (it
// carries the NT signature) and its empty [AddReg] adds nothing, so BaseHives can
// seed the SOFTWARE/DEFAULT/CLASSES hives in a hermetic test.
const minimalHiveINF = `[Version]
Signature = "$Windows NT$"

[AddReg]
`

const testISO = "../testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

// syntheticMeta is minimal source metadata: a directory table and a couple of Load
// classes, enough for layout.Parse to place files and derive drivers.
const syntheticMeta = `[Version]
Signature = "$Windows NT$"

[WinntDirectories]
1 = "\"
2 = system32
4 = system32\drivers

[FileSystems.Load]
ntfs = ntfs.sys

[SCSI.Load]
atapi = atapi.sys
cabonly = cabonly.sys
`

// TestAssemblePipeline exercises the post-probe pipeline hermetically: it derives the
// base action set from synthetic metadata, materializes every base file loosely in
// the source, and asserts assemble places them all. It bypasses Probe (which gates on
// a real ntdll version resource) via the internal assemble entry point.
func TestAssemblePipeline(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}

	// setupreg.hiv is a real (empty) regf image; the three hive*.inf files seed the
	// remaining base hives. Together they let the hive stage run hermetically.
	sysHive, err := hive.WriteHive(hive.NewHive("$$$PROTO.HIV"))
	if err != nil {
		t.Fatal(err)
	}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
		"i386/setupreg.hiv": {Data: sysHive},
		"i386/hivesft.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivedef.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivesys.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivecls.inf":  {Data: []byte(minimalHiveINF)},
	}

	// Populate every base copy file loosely, plus one of the two SCSI drivers so the
	// optional-skip path (cabonly.sys, absent) is exercised too.
	l, err := layout.Parse(backing, id)
	if err != nil {
		t.Fatal(err)
	}
	acts, err := base.Actions(nil, l, id)
	if err != nil {
		t.Fatal(err)
	}
	// cabonly.sys stays absent (exercises the optional-skip path); txtsetup.sif keeps
	// its real metadata content rather than being clobbered with a stub, since the
	// hive/layout stages re-read it.
	present := map[string]bool{"cabonly.sys": false, "txtsetup.sif": true}
	for _, a := range acts {
		if _, decided := present[a.Src]; decided {
			continue
		}
		backing["i386/"+a.Src] = &fstest.MapFile{Data: []byte("stub:" + a.Src)}
	}

	out := t.TempDir()
	rep, err := assemble(context.Background(), backing, id, out, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected per-file errors: %v", rep.Errors)
	}

	tree := walkTree(t, out)
	summary := fmt.Sprintf("identity: %s\nwritten: %d\nskipped: %v\nhives: %v\n\n%s",
		rep.Identity, rep.Written, rep.Skipped, rep.Hives, tree)
	golden.Assert(t, "synthetic-tree", summary)
}

// TestAssembleWithPlugin exercises plugin evaluation end-to-end through the pipeline:
// an enabled plugin ships a file (placed from the plugin, not the source) and writes a
// SOFTWARE registry value (merged into the built hive on top of the base layer).
func TestAssembleWithPlugin(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}

	sysHive, err := hive.WriteHive(hive.NewHive("$$$PROTO.HIV"))
	if err != nil {
		t.Fatal(err)
	}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
		"i386/setupreg.hiv": {Data: sysHive},
		"i386/hivesft.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivedef.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivesys.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivecls.inf":  {Data: []byte(minimalHiveINF)},
	}
	l, err := layout.Parse(backing, id)
	if err != nil {
		t.Fatal(err)
	}
	baseActs, err := base.Actions(nil, l, id)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{"cabonly.sys": false, "txtsetup.sif": true}
	for _, a := range baseActs {
		if _, decided := present[a.Src]; decided {
			continue
		}
		backing["i386/"+a.Src] = &fstest.MapFile{Data: []byte("stub:" + a.Src)}
	}

	// A plugin that ships demo.exe and adds a SOFTWARE registry value, plus a manifest
	// override that sets a second SOFTWARE value.
	demo := &plugin.Discovered{
		Name: "demo",
		FS:   fstest.MapFS{"demo.exe": &fstest.MapFile{Data: []byte("demo payload\n")}},
		Units: []*plugin.Plugin{{Name: "demo", Actions: []action.Action{
			{Kind: action.CopyFile, Src: "demo.exe", DirID: "2", ShipWithPlugin: true},
			{Kind: action.AddReg, Hive: action.HiveSoftware, Subkey: `PeBuilder\Demo`,
				ValueName: "Installed", RegType: action.RegSZ, Data: "yes", HasValue: true},
		}}},
	}
	// A local file for the inject override.
	injDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(injDir, "hello.cmd"), []byte("@echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selected := []plugin.Selected{{
		Plugin: demo,
		Override: plugin.Override{
			RegSet: map[string]string{`HKLM\SOFTWARE\PeBuilder\Demo\Extra`: "ovr"},
			Inject: []plugin.Inject{{From: filepath.Join(injDir, "hello.cmd"), To: "i386/system32/hello.cmd"}},
		},
	}}

	out := t.TempDir()
	rep, err := assemble(context.Background(), backing, id, out, selected, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected per-file errors: %v", rep.Errors)
	}

	// The plugin's shipped file is placed from the plugin, not fetched from the source.
	got, err := os.ReadFile(filepath.Join(out, "i386", "system32", "demo.exe"))
	if err != nil {
		t.Fatalf("demo.exe not placed: %v", err)
	}
	if string(got) != "demo payload\n" {
		t.Errorf("demo.exe = %q, want the plugin payload", got)
	}

	// The plugin's registry value merged into the SOFTWARE hive.
	swData, err := os.ReadFile(filepath.Join(out, "i386", "system32", "config", "SOFTWARE"))
	if err != nil {
		t.Fatalf("read SOFTWARE hive: %v", err)
	}
	h, err := hive.ReadHive(swData)
	if err != nil {
		t.Fatalf("parse SOFTWARE hive: %v", err)
	}
	k, ok := h.Root.FindPath(`PeBuilder\Demo`)
	if !ok {
		t.Fatal("plugin registry subkey PeBuilder\\Demo not found in SOFTWARE hive")
	}
	if v, ok := k.Value("Installed"); !ok || v.AsString() != "yes" {
		t.Errorf("Installed value = %q (ok=%v), want \"yes\"", v.AsString(), ok)
	}
	// The manifest override's registry.set value landed too.
	if v, ok := k.Value("Extra"); !ok || v.AsString() != "ovr" {
		t.Errorf("override Extra value = %q (ok=%v), want \"ovr\"", v.AsString(), ok)
	}
	// The injected local file was placed at its target path.
	inj, err := os.ReadFile(filepath.Join(out, "i386", "system32", "hello.cmd"))
	if err != nil {
		t.Fatalf("injected file not placed: %v", err)
	}
	if string(inj) != "@echo hi\n" {
		t.Errorf("injected file = %q, want the local file contents", inj)
	}
}

// TestSelectPlugins covers the build-layer glue that turns a manifest's enabled
// plugins into compiled actions: search-path assembly, discovery, selection, and
// compilation from a real on-disk plugin directory.
func TestSelectPlugins(t *testing.T) {
	// Keep the global and env tiers out of the way so only the project dir resolves.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PEBUILD_PLUGIN_PATH", "")

	dir := t.TempDir()
	pdir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	inf := `[Version]
Signature="$Windows NT$"
[PEBuilder]
Name="Demo"
Enable=1
[SourceDisksFiles]
demo.exe=2,,1
[Software.AddReg]
0x1,"PeBuilder\Demo","Installed","yes"
`
	if err := os.WriteFile(filepath.Join(pdir, "demo.inf"), []byte(inf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "demo.exe"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stub the external plugins the built-in wim-cd-boot meta-plugin depends on, so its
	// transitive closure resolves under layout=wim. A real WIM build installs these from
	// the wimmf/firadisk/grub4dos packages; here only their identity (the directory name the
	// `depends` list names) matters, so a bare enable-off .inf suffices.
	for _, dep := range []string{"wimmf", "firadisk", "grub4dos"} {
		ddir := filepath.Join(dir, dep)
		if err := os.MkdirAll(ddir, 0o755); err != nil {
			t.Fatal(err)
		}
		depInf := "[Version]\nSignature=\"$Windows NT$\"\n[PEBuilder]\nName=\"" + dep + "\"\nEnable=0\n"
		if err := os.WriteFile(filepath.Join(ddir, dep+".inf"), []byte(depInf), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// No enabled plugins: the base layer is the whole build.
	if sel, err := selectPlugins(&manifest.Manifest{PluginsDir: dir}, source.Identity{Build: 2600}); err != nil || sel != nil {
		t.Fatalf("no enabled plugins: sel=%v err=%v", sel, err)
	}

	// Enabled plugin resolves to a selection carrying its override layer.
	m := &manifest.Manifest{PluginsDir: dir, Plugins: []manifest.Plugin{
		{Name: "demo", Enabled: true, Files: manifest.Files{Exclude: []string{"i386/system32/gone.dll"}}},
	}}
	sel, err := selectPlugins(m, source.Identity{Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel) != 1 || sel[0].Plugin.Name != "demo" {
		t.Fatalf("selection = %+v, want one plugin 'demo'", sel)
	}
	if got := sel[0].Override.Exclude; len(got) != 1 || got[0] != "i386/system32/gone.dll" {
		t.Errorf("override.Exclude = %v, want the manifest exclude carried through", got)
	}

	// An enabled plugin that is not on the search path is a clear error.
	missing := &manifest.Manifest{PluginsDir: dir, Plugins: []manifest.Plugin{{Name: "ghost", Enabled: true}}}
	if _, err := selectPlugins(missing, source.Identity{Build: 2600}); err == nil {
		t.Error("enabled-but-absent plugin returned no error")
	}

	// The WIM layout implicitly enables the built-in wim-cd-boot meta-plugin, even with no
	// manifest plugin block — so a WIM build needs only `layout = "wim"`.
	wim := &manifest.Manifest{PluginsDir: dir, Layout: manifest.LayoutWIM}
	wsel, err := selectPlugins(wim, source.Identity{Build: 2600})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range wsel {
		if strings.EqualFold(s.Plugin.Name, "wim-cd-boot") {
			found = true
		}
	}
	if !found {
		t.Fatalf("layout=wim selection %+v does not include the implicit wim-cd-boot plugin", wsel)
	}

	// Explicitly disabling wim-cd-boot under layout=wim is a contradiction and must fail
	// with a clear message, not the resolver's generic disabled-dependency error.
	conflict := &manifest.Manifest{PluginsDir: dir, Layout: manifest.LayoutWIM,
		Plugins: []manifest.Plugin{{Name: "wim-cd-boot", Enabled: false}}}
	_, err = selectPlugins(conflict, source.Identity{Build: 2600})
	if err == nil || !strings.Contains(err.Error(), "requires the \"wim-cd-boot\" plugin") {
		t.Fatalf("disabled wim-cd-boot under layout=wim: err = %v, want a clear requirement error", err)
	}
}

// buildCapable makes a build-capable selected plugin: a source dir + a [Build] that
// produces tool.exe, and a [SourceDisksFiles] copy of it into system32.
func buildCapable(t *testing.T, provenance string) plugin.Selected {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "tool.c"), []byte("int main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &plugin.Discovered{
		Name:   "tool",
		Source: provenance,
		FS:     os.DirFS(src),
		Units: []*plugin.Plugin{{
			Name:  "tool",
			Build: plugin.BuildSpec{Present: true, Produces: []string{"tool.exe"}, Command: "printf TOOL > tool.exe"},
			Actions: []action.Action{
				{Kind: action.CopyFile, Src: "tool.exe", DirID: "2"},
			},
		}},
	}
	return plugin.Selected{Plugin: d}
}

func TestRunPluginBuildsTrustGate(t *testing.T) {
	// A user-supplied build-capable plugin is refused without the opt-in.
	user := []plugin.Selected{buildCapable(t, "project")}
	if _, err := runPluginBuilds(context.Background(), user, &manifest.Manifest{}, "i386"); err == nil {
		t.Error("user-supplied [Build] ran without allow_plugin_builds")
	}
	// With the opt-in it builds.
	user = []plugin.Selected{buildCapable(t, "project")}
	m := &manifest.Manifest{Build: manifest.Build{AllowPluginBuilds: true}}
	cleanup, err := runPluginBuilds(context.Background(), user, m, "i386")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if user[0].Built == nil {
		t.Error("opted-in user build produced no Built result")
	}

	// A built-in build-capable plugin builds automatically, no opt-in.
	builtin := []plugin.Selected{buildCapable(t, plugin.BuiltinLabel)}
	cleanup2, err := runPluginBuilds(context.Background(), builtin, &manifest.Manifest{}, "i386")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if builtin[0].Built == nil {
		t.Error("built-in build produced no Built result")
	}
}

func TestRunPluginBuildsScratchOutsideWorkDir(t *testing.T) {
	// The work_dir doubles as the output tree the media stage masters wholesale, so the
	// plugin-build scratch directory must not land inside it, or it would leak into the
	// ISO root. It builds in a system temp directory instead, leaving work_dir untouched.
	workDir := t.TempDir()
	selected := []plugin.Selected{buildCapable(t, plugin.BuiltinLabel)}
	m := &manifest.Manifest{Build: manifest.Build{WorkDir: workDir}}

	cleanup, err := runPluginBuilds(context.Background(), selected, m, "i386")
	if err != nil {
		t.Fatalf("runPluginBuilds errored: %v", err)
	}
	defer cleanup()
	if selected[0].Built == nil {
		t.Error("build produced no result")
	}
	// While the build is live (before cleanup), nothing has appeared under work_dir.
	ents, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("work_dir holds %d entries, want none; build scratch leaked into the output tree: %v", len(ents), ents)
	}
}

// TestAssembleBuiltPlugin runs the [Build] pre-pass and assembly together: a
// build-capable plugin compiles tool.exe, which is placed into the tree from the build
// output rather than the Windows source.
func TestAssembleBuiltPlugin(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	sysHive, err := hive.WriteHive(hive.NewHive("$$$PROTO.HIV"))
	if err != nil {
		t.Fatal(err)
	}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
		"i386/setupreg.hiv": {Data: sysHive},
		"i386/hivesft.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivedef.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivesys.inf":  {Data: []byte(minimalHiveINF)},
		"i386/hivecls.inf":  {Data: []byte(minimalHiveINF)},
	}
	l, err := layout.Parse(backing, id)
	if err != nil {
		t.Fatal(err)
	}
	baseActs, err := base.Actions(nil, l, id)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{"cabonly.sys": false, "txtsetup.sif": true}
	for _, a := range baseActs {
		if _, decided := present[a.Src]; decided {
			continue
		}
		backing["i386/"+a.Src] = &fstest.MapFile{Data: []byte("stub:" + a.Src)}
	}

	selected := []plugin.Selected{buildCapable(t, plugin.BuiltinLabel)}
	cleanup, err := runPluginBuilds(context.Background(), selected, &manifest.Manifest{}, "i386")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	out := t.TempDir()
	rep, err := assemble(context.Background(), backing, id, out, selected, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("per-file errors: %v", rep.Errors)
	}
	got, err := os.ReadFile(filepath.Join(out, "i386", "system32", "tool.exe"))
	if err != nil {
		t.Fatalf("built tool.exe not placed: %v", err)
	}
	if string(got) != "TOOL" {
		t.Errorf("tool.exe = %q, want the built payload TOOL", got)
	}
}

// TestBuildRealSource is the end-to-end check: build the real XP SP3 source
// and diff the assembled tree (sorted path + size) against a committed golden listing.
// It skips when the source ISO is absent, keeping CI hermetic.
func TestBuildRealSource(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	out := t.TempDir()
	m := &manifest.Manifest{
		Source: manifest.Source{Path: testISO, Type: "auto"},
		Build:  manifest.Build{WorkDir: out},
	}

	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("build reported per-file errors: %v", rep.Errors)
	}

	// The receipt lands at the tree root and describes this build: the source it read,
	// the manifest that drove it, and — with nothing but the base layer in play — a
	// clean taint verdict.
	rc := readReceipt(t, out)
	if rc.Source.Identity != rep.Identity.String() {
		t.Errorf("receipt source identity = %q, want %q", rc.Source.Identity, rep.Identity)
	}
	if want, err := enginehash.Sum(base.EmbedFS(), plugins.FS); err != nil {
		t.Fatal(err)
	} else if rc.PEBuild.EngineHash != want {
		t.Errorf("receipt engine hash = %q, want %q", rc.PEBuild.EngineHash, want)
	}
	if rc.Tainted {
		t.Errorf("a build with no plugin is tainted: %v", rc.Taint)
	}

	summary := fmt.Sprintf("identity: %s\nwritten: %d\nskipped: %s\n\n%s",
		rep.Identity, rep.Written, strings.Join(rep.Skipped, ","), walkTreeWithSizes(t, out))
	golden.Assert(t, "real-tree", summary)
}

// readReceipt reads and parses the build receipt at the root of an assembled tree.
func readReceipt(t *testing.T, treeDir string) *receipt.Receipt {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(treeDir, receipt.FileName))
	if err != nil {
		t.Fatalf("read build receipt: %v", err)
	}
	rc, err := receipt.Parse(data)
	if err != nil {
		t.Fatalf("parse build receipt: %v", err)
	}
	return rc
}

// TestBuildWin32GUIClosure exercises a plugin's [Closure] section end-to-end: enabling
// win32-gui computes the shared GUI runtime from the source's own import graph, so the
// seed roots and their static dependencies all land in the tree — none hand-listed.
func TestBuildWin32GUIClosure(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	out := t.TempDir()
	m := &manifest.Manifest{
		Source:  manifest.Source{Path: testISO, Type: "auto"},
		Output:  manifest.Output{Target: manifest.TargetNone},
		Build:   manifest.Build{WorkDir: out, KeepWork: true},
		Plugins: []manifest.Plugin{{Name: "win32-gui", Enabled: true}},
	}
	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("build reported per-file errors: %v", rep.Errors)
	}
	// The [Closure] seed roots (placed because a plugin owns its seeds) and a
	// representative computed dependency all reach system32.
	for _, f := range []string{
		"shell32.dll", "comctl32.dll", "shlwapi.dll", "comdlg32.dll", "ole32.dll", "oleaut32.dll",
	} {
		if _, err := os.Stat(filepath.Join(out, "i386", "system32", f)); err != nil {
			t.Errorf("win32-gui [Closure] did not place %s: %v", f, err)
		}
	}
	// The [SourcePaths] Common-Controls 6.0 side-by-side assembly lands in WinSxS: the v6
	// comctl32 and its manifest, at the exact hash-encoded paths the loader resolves.
	asm := "x86_Microsoft.Windows.Common-Controls_6595b64144ccf1df_6.0.0.0_x-ww_1382d70a"
	for _, p := range []string{
		filepath.Join("i386", "WinSxS", asm, "comctl32.dll"),
		filepath.Join("i386", "WinSxS", "Manifests", asm+".manifest"),
	} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("win32-gui [SourcePaths] did not stage the WinSxS assembly file %s: %v", p, err)
		}
	}
}

// TestBuildISO is the end-to-end media check: build the real XP SP3 source with the
// iso target and confirm the produced image is bootable (El Torito boot record) and
// carries the boot chain — the loader at \I386, txtsetup.sif switched to /minint, and
// the SYSTEM hive — all readable back through the ISO.
func TestBuildISO(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	out := filepath.Join(t.TempDir(), "rescue.iso")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: testISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetISO, Path: out, VolumeLabel: "PEBUILDER"},
		Build:  manifest.Build{WorkDir: filepath.Join(t.TempDir(), "tree")},
	}

	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath != out || rep.ISOSize == 0 {
		t.Fatalf("report ISO = %q (%d bytes)", rep.ISOPath, rep.ISOSize)
	}

	// The image has a readable El Torito no-emulation boot record.
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := boot.ExtractBootImage(f); err != nil {
		t.Errorf("boot record not readable: %v", err)
	}

	// Read the boot chain back through the ISO filesystem.
	src, err := source.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	b := src.Backing()
	if _, err := fs.ReadFile(b, "i386/setupldr.bin"); err != nil {
		t.Errorf("\\I386\\SETUPLDR.BIN not in ISO: %v", err)
	}
	// The boot loads a full set of hives. In /minint mode the SYSTEM hive is
	// system32\SETUPREG.HIV (not the installed-Windows config\SYSTEM path); SOFTWARE
	// and the default-user hive sit in config; and empty-but-valid SAM and SECURITY
	// hives must be present in config or the config manager bugchecks (0xc0000218).
	for _, hive := range []string{
		"i386/system32/setupreg.hiv",
		"i386/system32/config/software",
		"i386/system32/config/default",
		"i386/system32/config/sam",
		"i386/system32/config/security",
	} {
		if _, err := fs.ReadFile(b, hive); err != nil {
			t.Errorf("hive %s not in ISO: %v", hive, err)
		}
	}
	txt, err := fs.ReadFile(b, "i386/txtsetup.sif")
	if err != nil {
		t.Fatalf("txtsetup.sif not in ISO: %v", err)
	}
	if !strings.Contains(string(txt), "/minint") {
		t.Error("txtsetup.sif in ISO does not carry the /minint switch")
	}

	// The receipt travels inside the image, at its root, so a copied or renamed ISO
	// still says what it was built from. The media stage uppercases the whole tree, so
	// on the disc it is PEBUILD.TXT — and the booted PE can `type X:\PEBUILD.TXT`.
	data, err := fs.ReadFile(b, "PEBUILD.TXT")
	if err != nil {
		t.Fatalf("PEBUILD.TXT not at the ISO root: %v", err)
	}
	rc, err := receipt.Parse(data)
	if err != nil {
		t.Fatalf("receipt in ISO does not parse: %v", err)
	}
	if rc.Output.Target != manifest.TargetISO || rc.Output.VolumeLabel != "PEBUILDER" {
		t.Errorf("receipt output = %+v, want the iso target with volume PEBUILDER", rc.Output)
	}
}

// TestBuildNone checks that the none target produces a complete PE tree — boot
// configuration applied (txtsetup.sif /minint, startnet.cmd) — but no media.
func TestBuildNone(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	out := filepath.Join(t.TempDir(), "tree")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: testISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetNone},
		Build:  manifest.Build{WorkDir: out},
	}
	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath != "" {
		t.Errorf("none target produced media: %q", rep.ISOPath)
	}
	// The tree survives and carries its boot configuration.
	txt, err := os.ReadFile(filepath.Join(out, "i386", "txtsetup.sif"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(txt), "/minint") {
		t.Error("none tree txtsetup.sif not switched to /minint")
	}
	if _, err := os.Stat(filepath.Join(out, "i386", "system32", "startnet.cmd")); err != nil {
		t.Errorf("none tree missing startnet.cmd: %v", err)
	}
}

// TestBuildISOKeepWork checks that with keep_work the ISO is mastered from a clone,
// leaving the work tree intact.
func TestBuildISOKeepWork(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	tree := filepath.Join(t.TempDir(), "tree")
	out := filepath.Join(t.TempDir(), "rescue.iso")
	m := &manifest.Manifest{
		Source: manifest.Source{Path: testISO, Type: "auto"},
		Output: manifest.Output{Target: manifest.TargetISO, Path: out, VolumeLabel: "PEBUILDER"},
		Build:  manifest.Build{WorkDir: tree, KeepWork: true},
	}
	rep, err := Build(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ISOPath == "" || rep.OutDir != tree {
		t.Fatalf("keep_work: ISOPath=%q OutDir=%q (want tree kept)", rep.ISOPath, rep.OutDir)
	}
	// Both the image and the (non-consumed) tree exist.
	if _, err := os.Stat(out); err != nil {
		t.Errorf("iso not produced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "i386", "setupldr.bin")); err != nil {
		t.Errorf("work tree not kept: %v", err)
	}
}

func TestBuildBadSource(t *testing.T) {
	m := &manifest.Manifest{Source: manifest.Source{Path: filepath.Join(t.TempDir(), "nope")}}
	if _, err := Build(context.Background(), m); err == nil {
		t.Fatal("Build with a nonexistent source returned no error")
	}
}

// TestAssembleCancelled checks that a cancelled context aborts the build fatally
// rather than being demoted to a non-fatal report error (which would let a cancelled
// build look successful).
func TestAssembleCancelled(t *testing.T) {
	id := source.Identity{Build: 2600, Arch: "i386", Flavor: "winxp"}
	backing := fstest.MapFS{
		"i386/txtsetup.sif": {Data: []byte(syntheticMeta)},
		"i386/layout.inf":   {Data: []byte(syntheticMeta)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := assemble(ctx, backing, id, t.TempDir(), nil, nil, false)
	if err == nil {
		t.Fatalf("assemble with a cancelled context returned no error (rep=%+v)", rep)
	}
}

// walkTree returns the sorted output-relative paths of files under root.
func walkTree(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n") + "\n"
}

// walkTreeWithSizes returns sorted "path size" lines for every file under root — the
// deterministic (decompressed) shape used for the golden tree diff.
func walkTreeWithSizes(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		// The build receipt records the build's own timestamp and the paths it read,
		// so its size varies per run; list it without one. What it contains is asserted
		// directly instead (TestBuildRealSource).
		if rel == receipt.FileName {
			lines = append(lines, rel+" (varies)")
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s %d", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}
