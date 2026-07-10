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

// RegHive names the target registry hive of a registry action.
type RegHive string

// The registry hives a plugin section can target (chosen by the section prefix).
const (
	HiveSoftware RegHive = "software" // HKLM\Software
	HiveSystem   RegHive = "system"   // the SYSTEM hive
	HiveDefault  RegHive = "default"  // the default-user hive
)

// Registry value types, matching the REG_* constants used in plugin AddReg lines.
const (
	RegSZ       = 0x1
	RegExpandSZ = 0x2
	RegBinary   = 0x3
	RegDWord    = 0x4
	RegMultiSZ  = 0x7
)

// Action is one step in the build IR. It is a flat, kind-tagged record: only the
// fields relevant to Kind are set. Kept as one struct (rather than an interface
// per kind) so action lists compare cleanly under go-cmp in tests.
type Action struct {
	// Kind selects how the action is executed and which fields below apply.
	Kind Kind

	// CopyFile / Mkdir: DirID is the target directory ID from the plugin's
	// [WinntDirectories] table (a numeric ID or a plugin-local a–z letter).
	DirID string

	// CopyFile fields ([SourceDisksFiles]).
	Src            string // source file name or wildcard pattern
	Rename         string // target name, if renamed
	NoDecompress   bool   // copy the compressed archive as-is (attribute 2)
	Optional       bool   // copy only if present (attribute 4)
	Uppercase      bool   // rename the target to uppercase (attribute 8)
	ShipWithPlugin bool   // file ships with the plugin; presence is required (attribute 1)
	// Origin names where a copy's bytes come from: empty means the Windows source
	// (loose file / archive / driver cabinet); a non-empty key selects a plugin's own
	// directory (a shipped file) or, later, a plugin's build output — the tree
	// assembler is given a map from this key to the backing fs.FS, and consults it
	// before the Windows source.
	Origin string

	// Mkdir fields ([WinntDirectories]).
	Path         string // directory name/path
	AlwaysCreate bool   // create even when empty (attribute 1)
	FromRoot     bool   // create from the output root, not inside \i386 (attribute 2)

	// Registry fields (AddReg / DelReg / SetValue-of-registry).
	Hive      RegHive
	Subkey    string
	ValueName string // empty denotes the key's default value
	RegType   int    // REG_* type for AddReg; 0 for DelReg
	Data      string // textual value data (SZ/EXPAND_SZ/DWORD/MULTI_SZ)
	DataBytes []byte // raw value data for REG_BINARY
	// HasValue distinguishes an AddReg that writes a value (even an empty one)
	// from a key-only AddReg line that has no value-name field at all. Without it
	// an empty default value would be indistinguishable from "create the key only".
	HasValue bool

	// Text/INI patch fields (SetValue / AddLine / DelLine / Append).
	TargetFile    string // file to patch
	TargetSection string // section within the target file
	LineKey       string // SetValue key
	LineData      string // SetValue value, AddLine line, or DelLine prefix
	AppendFile    string // Append: file whose contents are appended to TargetFile
}
