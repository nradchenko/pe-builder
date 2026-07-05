// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Command pebuild builds bootable Windows PE environments from a user-supplied
// Windows installation source.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "pebuild:", err)
		os.Exit(1)
	}
}
