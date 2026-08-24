// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package base

import (
	"os"
	"testing"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/internal/repo"
	"github.com/nradchenko/pe-builder/plugin"
	"github.com/nradchenko/pe-builder/source"
)

// bootPromptINF is the plugin that stages the "press any key to boot from CD" file. Read by
// path rather than embedded because it belongs to another package; a failure to read it means
// the plugin moved or was removed, not that the base layer is broken.
func bootPromptINF(t testing.TB) string {
	t.Helper()
	return repo.Path(t, "plugins", "boot-prompt", "boot-prompt.inf")
}

// bootfixFile is the file the whole prompt consists of.
const bootfixFile = "bootfix.bin"

// TestBootPromptIsNotInBase asserts the base layer stages no bootfix.bin, on either
// architecture. The prompt is opt-in, and the base layer is what every build gets: an entry
// added back here would put it in every image and make the plugin a silent duplicate — two
// copy actions for one file, which the tree assembler resolves without complaining.
func TestBootPromptIsNotInBase(t *testing.T) {
	for _, arch := range []string{"i386", "amd64"} {
		acts, err := Actions(nil, fixtureLayout(), source.Identity{Flavor: "winxp", Build: 2600, Arch: arch})
		if err != nil {
			t.Fatalf("%s: base actions: %v", arch, err)
		}
		for _, a := range acts {
			if a.Kind == action.CopyFile && a.Src == bootfixFile {
				t.Errorf("%s: base layer stages %s; the boot prompt belongs to the boot-prompt "+
					"plugin, so that an image asks only when the manifest asked for it", arch, bootfixFile)
			}
		}
	}
}

// TestBootPromptStagesToTheBootArea compiles the plugin and checks the two properties that
// decide whether enabling it does anything at all.
//
// The boot area is the one that matters and the one nothing else would catch: on an amd64
// source the OS payload moves to AMD64\ while the real-mode boot chain stays in I386\, so a
// bootfix.bin staged without the boot-area attribute lands where the CD boot sector never
// looks. The build succeeds, the file is in the image, and the prompt never appears.
//
// Optional is the other half: a source that does not carry the file must still build.
func TestBootPromptStagesToTheBootArea(t *testing.T) {
	path := bootPromptINF(t)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n(this test pins that plugin's staging; if it was renamed or "+
			"removed, update or delete this test)", path, err)
	}
	f, err := inf.Parse(b)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	for _, arch := range []string{"i386", "amd64"} {
		acts, err := plugin.Actions(f, plugin.Target{Build: 2600, Arch: arch})
		if err != nil {
			t.Fatalf("%s: compile: %v", arch, err)
		}
		var got *action.Action
		for i, a := range acts {
			if a.Kind == action.CopyFile && a.Src == bootfixFile {
				got = &acts[i]
			}
		}
		if got == nil {
			t.Errorf("%s: plugin stages no %s, so enabling it produces no prompt", arch, bootfixFile)
			continue
		}
		if got.Area != source.AreaBoot {
			t.Errorf("%s: %s staged in the %v area; the CD boot sector reads it from the boot area "+
				"(I386) on every architecture", arch, bootfixFile, got.Area)
		}
		if !got.Optional {
			t.Errorf("%s: %s staged as required; a source that does not carry it must still build",
				arch, bootfixFile)
		}
	}
}
