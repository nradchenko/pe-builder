// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nradchenko/pe-builder/internal/action"
	"github.com/nradchenko/pe-builder/internal/manifest"
	"github.com/nradchenko/pe-builder/internal/plugin"
	"github.com/nradchenko/pe-builder/internal/receipt"
	"github.com/nradchenko/pe-builder/source"
)

// selectedPlugin builds a Selected for taint tests: origin label, whether it ships its
// own files, and whether its [Build] ran.
func selectedPlugin(name, origin string, ships bool, built bool) plugin.Selected {
	u := &plugin.Plugin{Name: name}
	if ships {
		u.Actions = []action.Action{{Kind: action.CopyFile, Src: name + ".sys", ShipWithPlugin: true}}
	}
	s := plugin.Selected{Plugin: &plugin.Discovered{
		Name:   name,
		Source: origin,
		FS:     fstest.MapFS{name + ".inf": {Data: []byte("[PEBuilder]\n")}},
		Units:  []*plugin.Plugin{u},
	}}
	if built {
		s.Built = &plugin.Built{Dir: fstest.MapFS{}}
	}
	return s
}

func TestTaintReasons(t *testing.T) {
	inject := &manifest.Manifest{Plugins: []manifest.Plugin{{
		Name: "acme", Enabled: true,
		Files: manifest.Files{Inject: []manifest.Inject{{From: "tool.exe", To: "i386/system32/tool.exe"}}},
	}}}

	cases := []struct {
		name     string
		m        *manifest.Manifest
		selected []plugin.Selected
		want     []string
	}{{
		name: "no plugins is clean",
		m:    &manifest.Manifest{},
	}, {
		// Taint is about provenance, not about whether code ran: a built-in that
		// compiles its own payload is still in-tree.
		name: "built-in build-capable plugin stays clean",
		m:    &manifest.Manifest{},
		selected: []plugin.Selected{
			selectedPlugin("poweroff", plugin.BuiltinLabel, false, true),
			selectedPlugin("luna", plugin.BuiltinLabel, true, false),
		},
	}, {
		name:     "an out-of-tree plugin taints by being used at all",
		m:        &manifest.Manifest{},
		selected: []plugin.Selected{selectedPlugin("acme", "project", false, false)},
		want:     []string{"out-of-tree:acme"},
	}, {
		name:     "running its build code is its own reason",
		m:        &manifest.Manifest{},
		selected: []plugin.Selected{selectedPlugin("acme", "project", false, true)},
		want:     []string{"out-of-tree:acme", "ran-build-code:acme"},
	}, {
		name:     "staging files it carried is its own reason",
		m:        &manifest.Manifest{},
		selected: []plugin.Selected{selectedPlugin("acme", "project", true, false)},
		want:     []string{"out-of-tree:acme", "shipped-binaries:acme"},
	}, {
		// inject{} is the user's explicit choice, so it is not blamed on the plugin
		// block it is written in — and it taints even with no plugin of its own.
		name:     "manifest inject taints on its own",
		m:        inject,
		selected: []plugin.Selected{selectedPlugin("acme", plugin.BuiltinLabel, false, false)},
		want:     []string{"manifest-inject"},
	}, {
		name: "reasons accumulate across plugins",
		m:    inject,
		selected: []plugin.Selected{
			selectedPlugin("luna", plugin.BuiltinLabel, true, false),
			selectedPlugin("acme", "project", true, true),
		},
		want: []string{"manifest-inject", "out-of-tree:acme", "ran-build-code:acme", "shipped-binaries:acme"},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taintReasons(tc.m, tc.selected)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("taint = %v, want %v", got, tc.want)
			}
		})
	}
}

// The reasons are sorted, so the same build writes the same receipt bytes however the
// resolver happened to order the plugins.
func TestTaintReasonsAreSorted(t *testing.T) {
	m := &manifest.Manifest{}
	forward := taintReasons(m, []plugin.Selected{
		selectedPlugin("zeta", "project", false, false),
		selectedPlugin("alpha", "project", false, false),
	})
	reverse := taintReasons(m, []plugin.Selected{
		selectedPlugin("alpha", "project", false, false),
		selectedPlugin("zeta", "project", false, false),
	})
	if !reflect.DeepEqual(forward, reverse) {
		t.Errorf("plugin order changed the taint list: %v vs %v", forward, reverse)
	}
}

func TestMakeReceipt(t *testing.T) {
	m := &manifest.Manifest{
		Source: manifest.Source{Path: "/isos/winxp.iso"},
		Output: manifest.Output{Target: manifest.TargetISO, VolumeLabel: "PEBUILDER"},
		Path:   "examples/luna.hcl",
		Digest: "sha256:deadbeef",
	}
	id := source.Identity{Build: 2600, Arch: "i386"}
	selected := []plugin.Selected{
		selectedPlugin("luna", plugin.BuiltinLabel, true, false),
		selectedPlugin("acme", "project", false, false),
	}

	rc, err := makeReceipt(m, id, selected, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Schema != receipt.Schema {
		t.Errorf("schema = %d, want %d", rc.Schema, receipt.Schema)
	}
	if rc.PEBuild.EngineHash == "" {
		t.Error("engine hash is empty")
	}
	if rc.Manifest.Digest != m.Digest || rc.Manifest.File != "luna.hcl" {
		t.Errorf("manifest = %+v, want luna.hcl / %s", rc.Manifest, m.Digest)
	}
	if rc.Source.Identity != id.String() || rc.Source.File != "winxp.iso" {
		t.Errorf("source = %+v", rc.Source)
	}
	if rc.Output.Target != manifest.TargetISO || rc.Output.VolumeLabel != "PEBUILDER" {
		t.Errorf("output = %+v", rc.Output)
	}

	// The resolved set is recorded in build order, and only the plugin pebuild does not
	// embed carries a content hash — the engine hash already covers the built-in.
	if len(rc.Plugins) != 2 || rc.Plugins[0].Name != "luna" || rc.Plugins[1].Name != "acme" {
		t.Fatalf("plugins = %+v", rc.Plugins)
	}
	if rc.Plugins[0].Origin != receipt.OriginBuiltin || rc.Plugins[0].ContentHash != "" {
		t.Errorf("built-in entry = %+v", rc.Plugins[0])
	}
	if rc.Plugins[1].Origin != receipt.OriginExternal || rc.Plugins[1].ContentHash == "" {
		t.Errorf("external entry = %+v", rc.Plugins[1])
	}

	if !rc.Tainted || rc.TaintLine() != "out-of-tree:acme" {
		t.Errorf("taint = %v %v", rc.Tainted, rc.Taint)
	}
}

// A receipt ships inside the image it describes, so it must not carry the build
// machine's directory layout out to whoever receives that image. Only base names are
// recorded — and this checks the whole rendered receipt, not just the two fields known
// to hold a name, so a field added later that leaks a path fails here.
func TestReceiptCarriesNoLocalPaths(t *testing.T) {
	dir := "/home/somebody/private/projects"
	m := &manifest.Manifest{
		Source: manifest.Source{Path: dir + "/isos/winxp.iso"},
		Output: manifest.Output{Target: manifest.TargetISO, VolumeLabel: "PEBUILDER"},
		Path:   dir + "/manifests/luna.hcl",
		Digest: "sha256:deadbeef",
		Plugins: []manifest.Plugin{{
			Name: "acme", Enabled: true,
			Files: manifest.Files{Inject: []manifest.Inject{{From: dir + "/tool.exe", To: "i386/system32/tool.exe"}}},
		}},
	}
	selected := []plugin.Selected{selectedPlugin("acme", "project", true, true)}

	rc, err := makeReceipt(m, source.Identity{Build: 2600, Arch: "i386"}, selected, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, err := rc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), dir) {
		t.Errorf("the receipt carries a local path:\n%s", data)
	}
	// The names themselves are kept: they are what a human reads to recognize a build,
	// and they say nothing about where the builder keeps their files.
	if rc.Manifest.File != "luna.hcl" || rc.Source.File != "winxp.iso" {
		t.Errorf("redaction dropped the file names too: %+v %+v", rc.Manifest, rc.Source)
	}
}

// A plugin directory is a place on someone's disk, and may hold something the build
// cannot read — a dangling symlink, a socket — that contributed nothing to the build. The
// receipt is composed after the tree is assembled, so failing there would discard a
// finished build over an irrelevant file. The hash is dropped instead.
func TestMakeReceiptSurvivesAnUnreadablePluginFile(t *testing.T) {
	s := selectedPlugin("acme", "project", true, false)
	s.Plugin.FS = unreadableFS{}

	rc, err := makeReceipt(&manifest.Manifest{}, source.Identity{Build: 2600, Arch: "i386"},
		[]plugin.Selected{s}, time.Now())
	if err != nil {
		t.Fatalf("an unreadable file in a plugin directory failed the build: %v", err)
	}
	if len(rc.Plugins) != 1 || rc.Plugins[0].ContentHash != "" {
		t.Errorf("expected the entry with no content hash, got %+v", rc.Plugins)
	}
	// The plugin is still recorded, and still taints — only its fingerprint is missing.
	if !rc.Tainted {
		t.Error("the plugin was dropped from the taint verdict")
	}
}

// unreadableFS lists a file that cannot then be read, the shape a dangling symlink takes
// through an fs.FS.
type unreadableFS struct{}

func (unreadableFS) Open(name string) (fs.File, error) {
	if name == "." {
		return fstest.MapFS{"broken": {Data: nil}}.Open(".")
	}
	return nil, errors.New("broken symlink")
}

// A build's receipt records the option values it was actually built with — the plugin's
// declared defaults overlaid with the manifest's overrides — since that is what makes
// "was this built at that setting?" answerable without booting the image.
func TestMakeReceiptRecordsEffectiveOptions(t *testing.T) {
	s := selectedPlugin("luna", plugin.BuiltinLabel, false, false)
	s.Plugin.Units[0].Options = []plugin.Option{
		{Name: "color", Default: "NormalColor"},
		{Name: "size", Default: "NormalSize"},
	}
	s.Override = plugin.Override{Options: map[string]string{"color": "Metallic"}}

	rc, err := makeReceipt(&manifest.Manifest{}, source.Identity{Build: 2600, Arch: "i386"},
		[]plugin.Selected{s}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"color": "Metallic", "size": "NormalSize"}
	if !reflect.DeepEqual(rc.Plugins[0].Options, want) {
		t.Errorf("options = %v, want %v", rc.Plugins[0].Options, want)
	}
}
