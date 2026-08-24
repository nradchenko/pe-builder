// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/manifest"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [manifest.hcl]",
		Short: "Parse and validate a build manifest and list its enabled plugins",
		Long: "Parse and validate a build manifest, reporting any schema or semantic\n" +
			"errors and listing the plugins it enables. With no argument, pebuild.hcl\n" +
			"in the current directory is used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			m, path, err := loadManifest(arg)
			if err != nil {
				return err
			}
			return printSummary(cmd.OutOrStdout(), path, m)
		},
	}
}

// printSummary writes a human-readable overview of a validated manifest.
func printSummary(w io.Writer, path string, m *manifest.Manifest) error {
	enabled := m.EnabledPlugins()

	var b strings.Builder
	fmt.Fprintf(&b, "Manifest: %s\n", path)
	fmt.Fprintf(&b, "Source:   %s (%s)\n", m.Source.Path, m.Source.Type)
	// What a target writes is a property of the target, not of whether a path happens
	// to be set: a "none" manifest may still carry a path (one written before the path
	// became optional, which validates fine), and reporting it would promise a file the
	// build never writes. The volume label is likewise unused rather than absent — it is
	// always defaulted, so it can only mislead here.
	if m.Output.Target == manifest.TargetNone {
		fmt.Fprintf(&b, "Output:   %s (file tree only; no media)\n", m.Output.Target)
	} else {
		fmt.Fprintf(&b, "Output:   %s -> %s (label %s)\n", m.Output.Target, m.Output.Path, m.Output.VolumeLabel)
	}
	fmt.Fprintf(&b, "Plugins:  %d enabled / %d total\n", len(enabled), len(m.Plugins))
	for _, p := range enabled {
		fmt.Fprintf(&b, "  - %s\n", p.Name)
	}

	_, err := io.WriteString(w, b.String())
	return err
}
