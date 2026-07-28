// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Command pebuild builds bootable Windows PE environments from a user-supplied
// Windows installation source.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		// A command that has already reported its outcome in full asks only for the
		// exit code (see errSilent); printing "pebuild: ..." after it would repeat a
		// verdict the user just read.
		if !errors.Is(err, errSilent) {
			fmt.Fprintln(os.Stderr, "pebuild:", err)
		}
		os.Exit(1)
	}
}
