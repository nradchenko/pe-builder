// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Discover and inspect plugins",
	}
	cmd.AddCommand(newPluginsListCmd(), newPluginsInfoCmd())
	return cmd
}

func newPluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [dir]",
		Short: "List discovered plugins",
		Long: "List the plugins discovered in a drop-in directory. With no argument,\n" +
			"the manifest's configured plugins directory is used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("plugins list: %w", errNotImplemented)
		},
	}
}

func newPluginsInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show parsed details for a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("plugins info: %w", errNotImplemented)
		},
	}
}
