// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build [manifest.hcl]",
		Short: "Build a bootable image from a manifest",
		Long: "Run the full build pipeline described by a manifest. With no argument,\n" +
			"pebuild.hcl in the current directory is used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("build: %w", errNotImplemented)
		},
	}
}
