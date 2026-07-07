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
	return &cobra.Command{
		Use:   "build [manifest.hcl]",
		Short: "Build the output file tree from a manifest",
		Long: "Run the build pipeline described by a manifest: read the Windows source,\n" +
			"evaluate the base layer, assemble the output file tree, and build the registry\n" +
			"hives. Boot configuration and ISO mastering arrive in a later stage — this\n" +
			"command produces the file tree and hives in the work directory, not a bootable\n" +
			"image yet.\n\n" +
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
}

// printReport writes a human-readable summary of a build.
func printReport(w io.Writer, rep *build.Report) error {
	fmt.Fprintf(w, "Source:  %s\n", rep.Identity)
	fmt.Fprintf(w, "Output:  %s (file tree)\n", rep.OutDir)
	fmt.Fprintf(w, "Files:   %d written, %d skipped\n", rep.Written, len(rep.Skipped))
	fmt.Fprintf(w, "Hives:   %d written\n", len(rep.Hives))
	if len(rep.Errors) > 0 {
		fmt.Fprintf(w, "Errors:  %d\n", len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Fprintf(w, "  - %v\n", e)
		}
	}
	fmt.Fprintln(w, "\nNote: boot configuration and ISO output arrive in a later stage;")
	fmt.Fprintln(w, "this is the assembled file tree and registry hives, not yet a bootable image.")
	return nil
}
