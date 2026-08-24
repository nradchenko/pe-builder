// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPathResolvesToCommittedFiles checks that the root the walk found is this
// repository's, by naming files that are committed here. go.mod is deliberately not one
// of them: the walk searched for it, so finding it again proves nothing.
func TestPathResolvesToCommittedFiles(t *testing.T) {
	for _, rel := range [][]string{
		{"internal", "repo", "repo.go"},
		{"CLAUDE.md"},
	} {
		if _, err := os.Stat(Path(t, rel...)); err != nil {
			t.Errorf("Path(%v): %v", rel, err)
		}
	}
}

// TestFindRootIsDepthIndependent walks from three different depths and requires the same
// answer. That independence is the point of the package — it is exactly the property a
// "../"-counted path lacks, and losing it is silent, so it is worth pinning.
func TestFindRootIsDepthIndependent(t *testing.T) {
	base := Root(t)

	var got []string
	for _, dir := range []string{".", "internal", filepath.Join("internal", "repo")} {
		t.Chdir(filepath.Join(base, dir))
		r, err := findRoot()
		if err != nil {
			t.Fatalf("findRoot from %s: %v", dir, err)
		}
		got = append(got, r)
	}
	// Compared against each other rather than against base: all three are read back the
	// same way, so a checkout reached through a symlink cannot make them disagree.
	for i, r := range got {
		if r != got[0] {
			t.Errorf("findRoot at depth %d = %q, want %q", i, r, got[0])
		}
	}
}

// TestDeclaresModuleRejectsAnotherModule covers the check that keeps a go.mod belonging
// to some enclosing project from being accepted as this repository's root.
func TestDeclaresModuleRejectsAnotherModule(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		name    string
		content string
		want    bool
	}{
		{"ours", "module " + module + "\n\ngo 1.25.0\n", true},
		{"tab separated", "module\t" + module + "\n", true},
		{"quoted", `module "` + module + `"` + "\n", true},
		{"trailing comment", "module " + module + " // the engine\n", true},
		{"block", "module (\n\t" + module + "\n)\n", true},
		{"block quoted", "module (\n\t\"" + module + "\"\n)\n", true},
		{"another module", "module example.com/other\n", false},
		{"quoted another module", `module "example.com/other"` + "\n", false},
		{"block another module", "module (\n\texample.com/other\n)\n", false},
		{"empty block", "module (\n)\n", false},
		{"prefix of ours", "module " + module + "/nested\n", false},
		{"named only in a comment", "// module " + module + "\nmodule example.com/other\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, tt.name+".mod")
			if err := os.WriteFile(p, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := declaresModule(p); got != tt.want {
				t.Errorf("declaresModule(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
