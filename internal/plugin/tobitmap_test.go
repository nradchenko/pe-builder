// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"strings"
	"testing"

	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/internal/action"
)

// actionsFor compiles a one-line [SourceDisksFiles] section and returns the copy it produced,
// or the error it refused with.
func actionsFor(t *testing.T, line string) ([]action.Action, error) {
	t.Helper()
	f, err := inf.Parse([]byte("[SourceDisksFiles]\n" + line + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return Actions(f, Target{Arch: "i386"})
}

// Attribute 64 marks a copy for re-encoding as a bitmap, and the rename field names what it
// lands as — the INF says the staged file is a bitmap, the engine only changes the bytes.
func TestToBitmapAttributeCompiles(t *testing.T) {
	acts, err := actionsFor(t, "bliss.jpg = 2,bliss.bmp,64")
	if err != nil {
		t.Fatal(err)
	}
	var got *action.Action
	for i := range acts {
		if acts[i].Kind == action.CopyFile {
			got = &acts[i]
		}
	}
	if got == nil {
		t.Fatal("no copy action produced")
	}
	if !got.ToBitmap {
		t.Errorf("ToBitmap = false, want true for attribute 64")
	}
	if got.Rename != "bliss.bmp" {
		t.Errorf("Rename = %q, want %q", got.Rename, "bliss.bmp")
	}
	// A file with no attribute must not acquire the transform.
	plain, err := actionsFor(t, "other.dll = 2")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plain {
		if a.ToBitmap {
			t.Errorf("a copy with no attribute was marked ToBitmap")
		}
	}
}

// Both conflicts are refused where the line and its plugin can still be named, rather than
// producing a file that is neither what was asked for nor usable.
func TestToBitmapConflictsAreRefused(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{"kept compressed", "bliss.jpg = 2,bliss.bmp,66", "kept compressed"},    // 2|64
		{"adapted as an INF", "bliss.jpg = 2,bliss.bmp,96", "adapted for a PE"}, // 32|64
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := actionsFor(t, tc.line)
			if err == nil {
				t.Fatalf("expected a refusal for %q", tc.line)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "bliss.jpg") {
				t.Errorf("error %q should name the conflict (%q) and the file", err, tc.want)
			}
		})
	}
}
