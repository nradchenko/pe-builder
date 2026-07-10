// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import "io/fs"

// BuiltinLabel is the provenance shown for pebuild's embedded built-in plugins.
const BuiltinLabel = "built-in"

// builtinFS holds pebuild's embedded built-in plugins. It is empty until built-in
// plugins ship; a later change points it at a go:embed of the built-in plugin tree.
var builtinFS fs.FS = emptyFS{}

// Builtin returns the source of pebuild's embedded built-in plugins — the lowest
// precedence in the search path, so any drop-in dir shadows a built-in of the same
// name.
func Builtin() Source { return EmbeddedSource{Label: BuiltinLabel, FS: builtinFS} }

// emptyFS is an fs.FS with no entries: the built-in source before any plugin is
// embedded. Its ErrNotExist is handled the same as an absent drop-in directory.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
