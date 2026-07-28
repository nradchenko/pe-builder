// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package enginehash

import (
	"strings"
	"testing"
	"testing/fstest"
)

func tree() fstest.MapFS {
	return fstest.MapFS{
		"base.inf":            {Data: []byte("[Version]\n")},
		"plugins/luna/a.inf":  {Data: []byte("luna\n")},
		"plugins/luna/b.c":    {Data: []byte("int main(void);\n")},
		"plugins/games/x.inf": {Data: []byte("games\n")},
	}
}

// The whole design rests on this: the same content hashes the same every time, so a
// no-op rebuild of pebuild does not report an image as stale.
func TestSumIsStable(t *testing.T) {
	a, err := Sum(tree())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Sum(tree())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("hashing the same tree twice differs:\n %s\n %s", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("digest %q is not sha256-tagged", a)
	}
}

// A map has no inherent order; the walk must impose one, or the digest would depend on
// iteration order rather than content.
func TestSumIgnoresIterationOrder(t *testing.T) {
	want, err := Sum(tree())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := Sum(tree())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("digest varies across runs: %s != %s", got, want)
		}
	}
}

func TestSumDetectsChanges(t *testing.T) {
	base, err := Sum(tree())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]func(fstest.MapFS){
		"changed content": func(f fstest.MapFS) { f["plugins/luna/a.inf"] = &fstest.MapFile{Data: []byte("LUNA\n")} },
		"renamed file": func(f fstest.MapFS) {
			f["plugins/luna/renamed.inf"] = f["plugins/luna/a.inf"]
			delete(f, "plugins/luna/a.inf")
		},
		"added file":   func(f fstest.MapFS) { f["plugins/new/n.inf"] = &fstest.MapFile{Data: []byte("new\n")} },
		"removed file": func(f fstest.MapFS) { delete(f, "plugins/games/x.inf") },
		// Length-prefixing is what stops a name/content boundary from sliding: moving a
		// byte from a path into the content it precedes must not hash the same.
		"content moved into path": func(f fstest.MapFS) {
			f["plugins/luna/a.in"] = &fstest.MapFile{Data: []byte("fluna\n")}
			delete(f, "plugins/luna/a.inf")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := tree()
			mutate(f)
			got, err := Sum(f)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Errorf("%s did not change the digest", name)
			}
		})
	}
}

// The trees are folded in the order given, so the caller's fixed order is what makes the
// combined digest reproducible — and two trees cannot be swapped without notice.
func TestSumCombinesTreesInOrder(t *testing.T) {
	a := fstest.MapFS{"x": {Data: []byte("1")}}
	b := fstest.MapFS{"y": {Data: []byte("2")}}
	ab, err := Sum(a, b)
	if err != nil {
		t.Fatal(err)
	}
	ba, err := Sum(b, a)
	if err != nil {
		t.Fatal(err)
	}
	if ab == ba {
		t.Error("tree order does not affect the digest; it must, or the order is not pinned")
	}
}

func TestSumBytes(t *testing.T) {
	if got, want := SumBytes(nil), SumBytes([]byte{}); got != want {
		t.Errorf("nil and empty differ: %s != %s", got, want)
	}
	if SumBytes([]byte("a")) == SumBytes([]byte("b")) {
		t.Error("different bytes hash the same")
	}
}
