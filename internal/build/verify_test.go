// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"strings"
	"testing"
	"time"

	"github.com/nradchenko/pe-builder/internal/receipt"
	"github.com/nradchenko/pe-builder/source"
)

func recorded() []receipt.Plugin {
	return []receipt.Plugin{
		{Name: "win32-gui", Origin: receipt.OriginBuiltin},
		{Name: "luna", Origin: receipt.OriginBuiltin,
			Options: map[string]string{"color": "NormalColor", "size": "NormalSize"}},
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		name, rec, cur string
		want           Status
	}{
		{"identical", "sha256:aa", "sha256:aa", OK},
		{"different", "sha256:aa", "sha256:bb", Changed},
		// Nothing recorded cannot be claimed as a match, and it is not evidence of a
		// difference either.
		{"nothing recorded", "", "sha256:bb", Unknown},
		{"nothing current", "sha256:aa", "", Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compare("f", tc.rec, tc.cur).Status; got != tc.want {
				t.Errorf("status = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSourceField(t *testing.T) {
	base := receipt.Source{
		Identity: "build 2600 (i386)",
		File:     "winxp.iso",
		Size:     617754624,
		MTime:    time.Date(2026, 7, 17, 19, 28, 0, 0, time.UTC),
	}

	if got := sourceField(base, base, true); got.Status != OK {
		t.Errorf("an unchanged source = %v, want OK", got.Status)
	}

	// An unreadable source is UNKNOWN, never CHANGED. The trap this guards is that an
	// Identity which was never probed does not render as the empty string — it renders
	// as "build 0 ()" — so a comparison against it would report a difference on the
	// strength of a value that was never obtained. Taken from the real type rather than
	// spelled out, so the test tracks it if that rendering ever changes.
	unprobed := receipt.Source{Identity: source.Identity{}.String()}
	if unprobed.Identity == "" {
		t.Fatal("an unprobed identity is empty; this test guards the case where it is not")
	}
	got := sourceField(base, unprobed, false)
	if got.Status != Unknown {
		t.Errorf("an unreadable source = %v, want UNKNOWN", got.Status)
	}
	if got.Value != base.Identity {
		t.Errorf("an unreadable source shows %q; it should show what the image recorded", got.Value)
	}

	for _, tc := range []struct {
		name string
		cur  receipt.Source
	}{
		{"identity", receipt.Source{Identity: "build 3790 (amd64)", File: base.File, Size: base.Size, MTime: base.MTime}},
		{"file", receipt.Source{Identity: base.Identity, File: "other.iso", Size: base.Size, MTime: base.MTime}},
		{"size", receipt.Source{Identity: base.Identity, File: base.File, Size: 1, MTime: base.MTime}},
		{"mtime", receipt.Source{Identity: base.Identity, File: base.File, Size: base.Size, MTime: base.MTime.Add(time.Hour)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sourceField(base, tc.cur, true)
			if f.Status != Changed {
				t.Errorf("a changed %s = %v, want CHANGED", tc.name, f.Status)
			}
			if len(f.Detail) == 0 {
				t.Errorf("a changed %s reported no detail saying what differs", tc.name)
			}
		})
	}
}

func TestPluginsField(t *testing.T) {
	rec := recorded()

	if got := pluginsField(rec, recorded(), true); got.Status != OK {
		t.Errorf("an unchanged plugin set = %v, want OK", got.Status)
	}
	if got := pluginsField(rec, nil, false); got.Status != Unknown {
		t.Errorf("an unresolvable plugin set = %v, want UNKNOWN", got.Status)
	}

	dropped := rec[:1]
	if got := pluginsField(rec, dropped, true); got.Status != Changed || len(got.Detail) == 0 {
		t.Errorf("a dropped plugin = %v %v, want CHANGED with detail", got.Status, got.Detail)
	}

	added := append(recorded(), receipt.Plugin{Name: "games", Origin: receipt.OriginBuiltin})
	if got := pluginsField(rec, added, true); got.Status != Changed {
		t.Errorf("an added plugin = %v, want CHANGED", got.Status)
	}

	// A plugin that changed hands — built in before, now from a drop-in directory — is a
	// different build even under the same name.
	moved := recorded()
	moved[1].Origin = receipt.OriginExternal
	moved[1].ContentHash = "sha256:aa"
	if got := pluginsField(rec, moved, true); got.Status != Changed {
		t.Errorf("a plugin that changed origin = %v, want CHANGED", got.Status)
	}

	// An out-of-tree plugin whose directory changed underneath the build.
	ext := []receipt.Plugin{{Name: "acme", Origin: receipt.OriginExternal, ContentHash: "sha256:aa"}}
	edited := []receipt.Plugin{{Name: "acme", Origin: receipt.OriginExternal, ContentHash: "sha256:bb"}}
	if got := pluginsField(ext, edited, true); got.Status != Changed {
		t.Errorf("an edited external plugin = %v, want CHANGED", got.Status)
	}

	// Plugins apply last-writer-wins, so the same set in a different order builds a
	// different image and must not read as a match.
	swapped := []receipt.Plugin{rec[1], rec[0]}
	if got := pluginsField(rec, swapped, true); got.Status != Changed {
		t.Errorf("a reordered plugin set = %v, want CHANGED", got.Status)
	}
}

func TestOptionsField(t *testing.T) {
	rec := recorded()

	if got := optionsField(rec, recorded(), true); got.Status != OK {
		t.Errorf("unchanged options = %v, want OK", got.Status)
	}
	if got := optionsField(rec, nil, false); got.Status != Unknown {
		t.Errorf("unresolved options = %v, want UNKNOWN", got.Status)
	}

	// The differing option is named — this is the question the field exists to answer.
	changed := recorded()
	changed[1].Options = map[string]string{"color": "Metallic", "size": "NormalSize"}
	got := optionsField(rec, changed, true)
	if got.Status != Changed {
		t.Fatalf("a changed option = %v, want CHANGED", got.Status)
	}
	if len(got.Detail) != 1 || !strings.Contains(got.Detail[0], "luna.color") ||
		!strings.Contains(got.Detail[0], "NormalColor") || !strings.Contains(got.Detail[0], "Metallic") {
		t.Errorf("detail = %v; it should name the option and both values", got.Detail)
	}
}

// A plugin gains and loses options between builds. Neither case may be described as a
// changed value: an option that did not exist was not "built with nothing", and one that
// is gone was not "changed to nothing" — an absent value and an empty one are different
// facts, and only one of them is something the build chose.
func TestOptionsFieldDistinguishesAbsentFromEmpty(t *testing.T) {
	withOpts := func(opts map[string]string) []receipt.Plugin {
		return []receipt.Plugin{{Name: "explorer-shell", Origin: receipt.OriginBuiltin, Options: opts}}
	}

	t.Run("option added since the build", func(t *testing.T) {
		got := optionsField(
			withOpts(map[string]string{"bpp": "32"}),
			withOpts(map[string]string{"bpp": "32", "unlock_taskbar": "0"}), true)
		if got.Status != Changed || len(got.Detail) != 1 {
			t.Fatalf("status = %v, detail = %v", got.Status, got.Detail)
		}
		if !strings.Contains(got.Detail[0], "not declared when this was built") {
			t.Errorf("detail = %q; a new option must not read as one built with an empty value", got.Detail[0])
		}
		if strings.Contains(got.Detail[0], `built with ""`) {
			t.Errorf("detail = %q; it claims the build chose an empty value", got.Detail[0])
		}
	})

	t.Run("option removed since the build", func(t *testing.T) {
		got := optionsField(
			withOpts(map[string]string{"bpp": "32", "legacy": "1"}),
			withOpts(map[string]string{"bpp": "32"}), true)
		if got.Status != Changed || len(got.Detail) != 1 {
			t.Fatalf("status = %v, detail = %v", got.Status, got.Detail)
		}
		if !strings.Contains(got.Detail[0], "no longer declared") {
			t.Errorf("detail = %q; a dropped option must say so", got.Detail[0])
		}
	})

	// An option genuinely set to the empty string is still a value the build chose, and
	// must keep comparing as one.
	t.Run("an empty value is still a value", func(t *testing.T) {
		if got := optionsField(withOpts(map[string]string{"note": ""}),
			withOpts(map[string]string{"note": ""}), true); got.Status != OK {
			t.Errorf("status = %v, want OK", got.Status)
		}
		got := optionsField(withOpts(map[string]string{"note": ""}),
			withOpts(map[string]string{"note": "x"}), true)
		if got.Status != Changed || !strings.Contains(got.Detail[0], `built with ""`) {
			t.Errorf("detail = %v; an empty recorded value is a value, not an absence", got.Detail)
		}
	})
}

// A verification is a match only when every graded field matched. An informational field
// never decides it, and an unknown one never counts as a pass — "verified" must not mean
// "verified except the parts I could not check".
func TestVerificationMatch(t *testing.T) {
	cases := []struct {
		name   string
		fields []Field
		want   bool
	}{
		{"all ok", []Field{{Status: OK}, {Status: OK}}, true},
		{"informational difference", []Field{{Status: OK}, {Status: Changed, Informational: true}}, true},
		{"one changed", []Field{{Status: OK}, {Status: Changed}}, false},
		{"one unknown", []Field{{Status: OK}, {Status: Unknown}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Verification{Fields: tc.fields}
			if got := v.Match(); got != tc.want {
				t.Errorf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}
