// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// This file is an external test package so it can reach layout's source reader, which
// imports inf. It exercises AdaptNetworkComponent against the real nettcpip.inf rather
// than a fixture: the transform's rules were derived from that file, and a fixture built
// from the same reading of it would agree with a misreading just as happily.
package inf_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/compress"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/source"
)

// testISO is the real XP source ISO symlinked under testdata/iso for local runs. Tests
// needing it skip when it is absent (e.g. CI), keeping the suite hermetic.
const testISO = "../testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso"

// The directives whose value is a list of section names, lowercased as Line.Key is not.
var sectionListKeys = []string{"addreg", "delreg", "bitreg", "ini2reg", "needs"}

func TestAdaptNetworkComponentOnRealINF(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skip("real source ISO not present under testdata/iso; skipping")
	}
	src, err := source.Open(testISO)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	id, err := src.Probe()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	raw, packed, found := layout.ReadSourceFile(src.Backing(), id.Roots(), source.AreaSystem, "nettcpip.inf")
	if !found {
		t.Fatal("nettcpip.inf not found on the source")
	}
	// The media ships it as nettcpip.in_, which is how the build stage receives it too.
	if packed {
		if raw, err = compress.DecompressBytes(raw); err != nil {
			t.Fatalf("decompress nettcpip.inf: %v", err)
		}
	}

	got := string(inf.Decode(inf.AdaptNetworkComponent(raw)))

	// Every descriptor is gone, in both the forms the file states them.
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "security") && strings.Contains(trimmed, "=") {
			t.Errorf("a Security= directive survived: %q", trimmed)
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(strings.ToLower(trimmed), ".security]") {
			t.Errorf("a descriptor section survived: %q", trimmed)
		}
	}

	// The key-creating sections beside them stay, keys and all. PersistentRoutes is the one
	// that matters: nothing else in the file creates it, so dropping this section left a
	// persistent route with no key to be written into.
	f, err := inf.Parse([]byte(got))
	if err != nil {
		t.Fatalf("the adapted INF no longer parses: %v", err)
	}
	for _, s := range []string{
		"Registry.Service.TCPIP.Parameters.Secure",
		"Registry.Service.TCPIP.Secure",
		"Registry.Service.NetBT.Parameters.Secure",
		"Registry.Service.DHCP.Secure",
	} {
		if !f.HasSection(s) {
			t.Errorf("key-creating section [%s] was dropped", s)
		}
	}
	if !strings.Contains(got, `Parameters\PersistentRoutes`) {
		t.Error("the PersistentRoutes key creation was lost")
	}

	// The invariant, checked against the whole real file rather than argued: no directive
	// names a section that is not there. A dangling reference fails the install outright.
	for _, sec := range f.Sections() {
		for _, line := range f.Section(sec, 0) {
			key := strings.ToLower(strings.TrimSpace(line.Key))
			if !contains(sectionListKeys, key) {
				continue
			}
			for _, name := range line.Fields() {
				name = strings.TrimSpace(name)
				if name == "" || f.HasSection(name) {
					continue
				}
				t.Errorf("[%s] %s names [%s], which the adapted file does not define",
					sec, key, name)
			}
		}
	}

	// ExcludeSetupStartServices carries the whole CoServices list, so the install starts none
	// of them. Read back through the parser, not matched as text, so the check is on the value
	// setupapi would see.
	coServices, exclude := ndiValues(t, f)
	if coServices == "" {
		t.Fatal("no CoServices value found in the adapted INF")
	}
	if exclude != coServices {
		t.Errorf("ExcludeSetupStartServices was not widened to CoServices:\n exclude = %q\n     co = %q",
			exclude, coServices)
	}
}

// ndiValues returns the component's CoServices and ExcludeSetupStartServices values, joined
// so two lists that name the same services in the same order compare equal.
func ndiValues(t *testing.T, f *inf.File) (coServices, exclude string) {
	t.Helper()
	for _, sec := range f.Sections() {
		for _, line := range f.Section(sec, 0) {
			fields := line.Fields()
			if len(fields) < 4 || !strings.EqualFold(strings.TrimSpace(fields[1]), "Ndi") {
				continue
			}
			value := strings.Join(fields[4:], ",")
			switch strings.ToLower(strings.TrimSpace(fields[2])) {
			case "coservices":
				coServices = value
			case "excludesetupstartservices":
				exclude = value
			}
		}
	}
	return coServices, exclude
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
