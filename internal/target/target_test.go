// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package target

import (
	"context"
	"slices"
	"testing"
)

// fakeTarget is a test-local Target implementation.
type fakeTarget struct {
	name string
}

func (f fakeTarget) Name() string { return f.name }

func (f fakeTarget) Emit(_ context.Context, _ string, _ Options) error { return nil }

func TestRegistry(t *testing.T) {
	// Registry is package-global; snapshot and restore around the test.
	saved := registry
	registry = map[string]Target{}
	t.Cleanup(func() { registry = saved })

	if names := Names(); len(names) != 0 {
		t.Fatalf("fresh registry has targets: %v", names)
	}
	if _, ok := Get("iso"); ok {
		t.Fatal("Get found a target in an empty registry")
	}

	Register(fakeTarget{name: "iso"})
	Register(fakeTarget{name: "usb"})

	if got, want := Names(), []string{"iso", "usb"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	got, ok := Get("iso")
	if !ok || got.Name() != "iso" {
		t.Errorf("Get(\"iso\") = %v, %v", got, ok)
	}

	// Registering the same name replaces the prior entry.
	Register(fakeTarget{name: "iso"})
	if got, want := len(Names()), 2; got != want {
		t.Errorf("after re-register, %d targets, want %d", got, want)
	}
}
