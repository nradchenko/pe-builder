// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package hive builds Windows registry hives (the regf on-disk format) from a
// source's base hive material and the merged registry actions, and reads an
// existing hive back into the same model.
//
// The core is a backend-agnostic in-memory tree (Hive/Key/Value). A reader
// parses an existing regf file into the tree; a pure-Go writer serializes a tree
// into a fresh, canonically-laid-out regf file. Registry semantics are honored:
// key and value names are matched case-insensitively (the canonical casing is
// preserved), and setting a value replaces any existing value of the same name.
//
// The structures a hive may contain follow the version it declares, not the
// newest form a reader might accept. These hives declare 1.3, so subkey indexes
// are written as fast leaves (lf), the form every 1.3 hive Microsoft ships uses
// and every NT reads; the hash leaf (lh) belongs to what XP's kernel writes, and
// a 1.3 hive carrying one is rejected outright by Windows 2000. A writer for a
// later version would choose lh there for the same reason — the rule is that the
// form follows the declared version, not that older is safer. Subkey count is the
// other axis: index roots (ri) are refused rather than approximated.
//
// Deliberate simplifications (safe for a RAM-resident, SYSTEM-run PE): a single
// shared permissive security descriptor is used for every key rather than
// per-key ACLs; and per-key class names, timestamps, and volatile flags are
// dropped. See the writer for the full list.
package hive

import "strings"

// Value is a single registry value: its name (empty means the key's default
// value), its REG_* type, and its raw on-disk data bytes. The bytes are the
// exact regf encoding of the value (SZ/EXPAND_SZ as UTF-16LE with a NUL
// terminator, DWORD as four little-endian bytes, MULTI_SZ as NUL-separated
// UTF-16LE strings with a double NUL, BINARY as-is); the encoders in values.go
// produce these bytes.
type Value struct {
	Name string
	Type uint32
	Data []byte
}

// Key is one registry key: its name (not the full path), its child keys, and its
// values. Subkey and value lookups fold case; the stored name keeps its casing.
type Key struct {
	Name    string
	Subkeys []*Key
	Values  []Value
}

// Hive is a registry hive: a single root key and everything under it.
type Hive struct {
	Root *Key
}

// NewHive returns an empty hive whose root key carries the given name.
func NewHive(rootName string) *Hive {
	return &Hive{Root: &Key{Name: rootName}}
}

// Child returns the subkey named name (case-insensitive), or nil if absent.
func (k *Key) Child(name string) *Key {
	for _, c := range k.Subkeys {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}

// EnsureChild returns the subkey named name, creating it if it does not exist.
// The first-seen casing of the name is preserved.
func (k *Key) EnsureChild(name string) *Key {
	if c := k.Child(name); c != nil {
		return c
	}
	c := &Key{Name: name}
	k.Subkeys = append(k.Subkeys, c)
	return c
}

// EnsurePath walks (creating as needed) a backslash-separated subkey path
// relative to k and returns the deepest key. Empty components are skipped, so a
// leading, trailing, or doubled separator is tolerated. An empty path returns k.
func (k *Key) EnsurePath(path string) *Key {
	cur := k
	for _, part := range strings.Split(path, `\`) {
		if part == "" {
			continue
		}
		cur = cur.EnsureChild(part)
	}
	return cur
}

// Value returns the value named name (case-insensitive) and whether it exists.
func (k *Key) Value(name string) (Value, bool) {
	for _, v := range k.Values {
		if strings.EqualFold(v.Name, name) {
			return v, true
		}
	}
	return Value{}, false
}

// SetValue adds v, or replaces the type and data of the existing value with the
// same name (case-insensitive) — last writer wins. As with EnsureChild, the
// first-seen casing of the name is preserved on replacement.
func (k *Key) SetValue(v Value) {
	for i := range k.Values {
		if strings.EqualFold(k.Values[i].Name, v.Name) {
			k.Values[i].Type = v.Type
			k.Values[i].Data = v.Data
			return
		}
	}
	k.Values = append(k.Values, v)
}

// DeleteChild removes the immediate subkey named name (case-insensitive) and
// reports whether one was removed.
func (k *Key) DeleteChild(name string) bool {
	for i, c := range k.Subkeys {
		if strings.EqualFold(c.Name, name) {
			k.Subkeys = append(k.Subkeys[:i], k.Subkeys[i+1:]...)
			return true
		}
	}
	return false
}

// DeleteValue removes the value named name (case-insensitive) and reports
// whether one was removed.
func (k *Key) DeleteValue(name string) bool {
	for i := range k.Values {
		if strings.EqualFold(k.Values[i].Name, name) {
			k.Values = append(k.Values[:i], k.Values[i+1:]...)
			return true
		}
	}
	return false
}

// FindPath walks a backslash-separated subkey path relative to k without
// creating anything, returning the deepest key and whether the whole path
// existed. Empty components are skipped. An empty path returns k.
func (k *Key) FindPath(path string) (*Key, bool) {
	cur := k
	for _, part := range strings.Split(path, `\`) {
		if part == "" {
			continue
		}
		cur = cur.Child(part)
		if cur == nil {
			return nil, false
		}
	}
	return cur, true
}
