// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/build"
	"github.com/nradchenko/pe-builder/manifest"
)

func newBuildCmd() *cobra.Command {
	var allowPluginBuilds bool
	cmd := &cobra.Command{
		Use:   "build [manifest.hcl]",
		Short: "Build a bootable image from a manifest",
		Long: "Run the build pipeline described by a manifest: read the Windows source,\n" +
			"evaluate the base layer, assemble the output file tree, build the registry\n" +
			"hives, apply the boot configuration, and master a bootable ISO. With\n" +
			"output.target = none the build stops after the tree and hives.\n\n" +
			"With no argument, pebuild.hcl in the current directory is used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			path, err := manifest.Resolve(arg)
			if err != nil {
				return err
			}
			m, err := manifest.Load(path)
			if err != nil {
				return fmt.Errorf("load %s: %w", path, err)
			}
			if err := manifest.Validate(m); err != nil {
				return fmt.Errorf("%s is invalid:\n%w", path, err)
			}
			// The flag lets a user-supplied plugin's [Build] run without editing the
			// manifest; either source opts in.
			if allowPluginBuilds {
				m.Build.AllowPluginBuilds = true
			}

			rep, err := build.Build(cmd.Context(), m)
			if err != nil {
				return fmt.Errorf("build: %w", err)
			}
			if err := printReport(cmd.OutOrStdout(), rep); err != nil {
				return err
			}
			// Surface per-file failures in the exit code so a script or CI job gating
			// on it does not treat an incomplete tree as a successful build.
			if n := len(rep.Errors); n > 0 {
				return fmt.Errorf("build completed with %d error(s)", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowPluginBuilds, "allow-plugin-builds", false,
		"allow a user-supplied plugin's [Build] section to run its command")
	return cmd
}

// printReport writes a human-readable summary of a build.
func printReport(w io.Writer, rep *build.Report) error {
	fmt.Fprintf(w, "Source:  %s\n", rep.Identity)
	fmt.Fprintf(w, "Files:   %d written, %d skipped\n", rep.Written, len(rep.Skipped))
	fmt.Fprintf(w, "Hives:   %d written\n", len(rep.Hives))
	if rep.ISOPath != "" {
		fmt.Fprintf(w, "ISO:     %s (%.1f MiB, volume %q)\n",
			rep.ISOPath, float64(rep.ISOSize)/(1024*1024), rep.VolumeLabel)
	} else {
		fmt.Fprintf(w, "Output:  %s (file tree; no media for the \"none\" target)\n", rep.OutDir)
	}
	if len(rep.Errors) > 0 {
		fmt.Fprintf(w, "Errors:  %d\n", len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Fprintf(w, "  - %v\n", e)
		}
	}
	return nil
}
