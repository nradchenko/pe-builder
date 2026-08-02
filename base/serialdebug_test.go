// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// serialDebugINF is the plugin whose loader options must track this package's base.inf. It is
// read by path rather than embedded because it belongs to another package; a failure to read it
// means the plugin moved or was removed, not that the base layer is broken.
const serialDebugINF = "../plugins/serialdebug/serialdebug.inf"

// debugSuffix is what the serialdebug plugin adds to the base layer's loader options.
const debugSuffix = " /debug /debugport=com1 /baudrate=115200"

// osloadoptionsRe pulls the value out of a SetValue line for the loader options, e.g.
//
//	"txtsetup.sif","SetupData","osloadoptions","""/fastdetect /minint …"""
//
// Only the doubled INF quoting is stripped; the value itself is returned as written.
var osloadoptionsRe = regexp.MustCompile(`^"txtsetup\.sif","SetupData","osloadoptions","""(.*)"""\s*$`)

// loaderOptions maps each INF section to the loader options it sets, e.g.
// "SetValue.i386" -> "/fastdetect /minint …". Keying by section rather than by position is the
// point: the architecture a value applies to comes from its section header, and a comparison
// that ignored that would accept two correct-looking strings attached to the wrong architectures.
func loaderOptions(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n(this test pins that plugin's loader options to base.inf; if the "+
			"plugin was renamed or removed, update or delete this test)", path, err)
	}
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			section = strings.Trim(s, "[]")
			continue
		}
		if m := osloadoptionsRe.FindStringSubmatch(s); m != nil {
			if prev, dup := out[section]; dup {
				t.Fatalf("%s: section [%s] sets osloadoptions twice (%q then %q)", path, section, prev, m[1])
			}
			out[section] = m[1]
		}
	}
	return out
}

// TestSerialDebugMatchesBase pins the serialdebug plugin to the base layer's loader options.
//
// The INF grammar can only SET a value, not append to one, so the plugin restates the base
// layer's whole osloadoptions string per architecture and adds the debug flags. Nothing at
// build time notices if the two drift: the plugin would keep setting a stale string and produce
// an image that boots differently from every other build — with, in the worst case, a debugger
// that never comes up, which is invisible precisely when it is needed.
//
// The comparison is keyed by section, so it also catches the two values being present but
// attached to the wrong architectures — which would boot each architecture with the other's HAL
// while every string in the file still looked right.
//
// If this fails because the base options changed deliberately, mirror the change into
// plugins/serialdebug/serialdebug.inf; the fix is never to relax the comparison.
func TestSerialDebugMatchesBase(t *testing.T) {
	baseOpts := loaderOptions(t, "base.inf")
	pluginOpts := loaderOptions(t, serialDebugINF)

	if len(baseOpts) == 0 {
		t.Fatal("no osloadoptions found in base.inf — has the line or its quoting changed?")
	}
	for section, want := range baseOpts {
		got, ok := pluginOpts[section]
		if !ok {
			t.Errorf("serialdebug has no [%s] section: it must set the loader options for every "+
				"architecture base.inf does, or that architecture boots without the debugger", section)
			continue
		}
		if got != want+debugSuffix {
			t.Errorf("[%s] drifted from the base layer:\n  base:   %q\n  plugin: %q\n  want:   %q",
				section, want, got, want+debugSuffix)
		}
	}
	for section := range pluginOpts {
		if _, ok := baseOpts[section]; !ok {
			t.Errorf("serialdebug sets osloadoptions in [%s], which base.inf does not — it would "+
				"override a value the base layer never set", section)
		}
	}
}
