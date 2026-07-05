// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Command pebuild builds bootable Windows PE environments from a user-supplied
// Windows installation source.
package main

import (
	"fmt"
	"os"

	"github.com/nradchenko/pe-builder/internal/buildinfo"
)

func main() {
	// Minimal entrypoint; the full cobra command tree is wired in separately.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(buildinfo.String())
		return
	}

	fmt.Fprintln(os.Stderr, "usage: pebuild version")
	os.Exit(2)
}
