// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Inspect a Windows installation source",
	}
	cmd.AddCommand(newSourceProbeCmd())
	return cmd
}

func newSourceProbeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "probe <path>",
		Short: "Detect a source's identity (build, arch, flavor)",
		Long: "Probe a Windows installation source (a directory or an .iso) and report\n" +
			"its detected identity: build number, architecture, and flavor.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("source probe: %w", errNotImplemented)
		},
	}
}
