// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package action defines the intermediate representation the build engine works
// in. Plugins and the base manifest compile to an ordered list of Actions, and
// manifest override layers add or suppress actions by target before they are
// executed.
//
// The per-kind action fields are filled in as later work implements tree, hive,
// and boot handling; this package currently defines the kinds and the Action
// envelope.
package action

import "slices"

// Kind identifies what an Action does.
type Kind string

// The action kinds emitted by plugin and manifest evaluation.
const (
	// CopyFile copies (and, where needed, decompresses) a source file into the tree.
	CopyFile Kind = "copy_file"
	// Mkdir creates a directory in the output tree.
	Mkdir Kind = "mkdir"
	// AddReg writes a registry key or value.
	AddReg Kind = "add_reg"
	// DelReg removes a registry key or value.
	DelReg Kind = "del_reg"
	// SetValue sets a single registry value.
	SetValue Kind = "set_value"
	// AddLine appends a line to a text/INF file.
	AddLine Kind = "add_line"
	// DelLine removes a line from a text/INF file.
	DelLine Kind = "del_line"
	// Append appends content (e.g. nu2menu XML) to a file.
	Append Kind = "append"
)

// Kinds lists every defined Kind, in evaluation-friendly order.
var Kinds = []Kind{CopyFile, Mkdir, AddReg, DelReg, SetValue, AddLine, DelLine, Append}

// Valid reports whether k is a defined Kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds, k)
}

// Action is one step in the build IR. Its per-kind payload fields are added as the
// executing stages are implemented.
type Action struct {
	// Kind selects how the action is executed.
	Kind Kind
}
