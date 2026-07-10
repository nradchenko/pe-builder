// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import "github.com/nradchenko/pe-builder/plugins"

// BuiltinLabel is the provenance shown for pebuild's embedded built-in plugins.
const BuiltinLabel = "built-in"

// Builtin returns the source of pebuild's embedded built-in plugins — the lowest
// precedence in the search path, so any drop-in dir shadows a built-in of the same
// name. The set is embedded from the plugins package (docs/plugins/inclusion-policy.md
// describes what may be bundled).
func Builtin() Source { return EmbeddedSource{Label: BuiltinLabel, FS: plugins.FS} }
