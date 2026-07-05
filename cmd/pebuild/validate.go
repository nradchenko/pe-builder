// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [manifest.hcl]",
		Short: "Parse and validate a build manifest and list its enabled plugins",
		Long: "Parse and validate a build manifest, reporting any schema or semantic\n" +
			"errors and listing the plugins it enables. With no argument, pebuild.hcl\n" +
			"in the current directory is used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("validate: %w", errNotImplemented)
		},
	}
}
