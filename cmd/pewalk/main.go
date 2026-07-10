// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Command pewalk computes the transitive PE import closure of a Windows binary
// against an installation source (a file, directory, or ISO — including
// compressed and cabinet-packed modules) and renders it as text, Graphviz DOT, or
// JSON. It is the "ldd for PE".
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "pewalk:", err)
		os.Exit(1)
	}
}
