// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/buildinfo"
)

// newRootCmd builds the full command tree. It is a constructor so tests can
// exercise an isolated command instance.
func newRootCmd() *cobra.Command {
	var verbose bool

	root := &cobra.Command{
		Use:   "pebuild",
		Short: "Build bootable Windows PE environments from a Windows installation source",
		Long: "pebuild assembles a bootable Windows PE environment from a user-supplied\n" +
			"Windows installation source, compatible with the classic Bart's PE Builder\n" +
			"plugin format.",
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			setupLogging(verbose)
		},
	}

	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	root.SetVersionTemplate(buildinfo.String("pebuild") + "\n")

	root.AddCommand(
		newVersionCmd(),
		newValidateCmd(),
		newBuildCmd(),
		newSourceCmd(),
		newPluginsCmd(),
	)

	return root
}

// Execute runs the root command against os.Args.
func Execute() error {
	return newRootCmd().Execute()
}

// setupLogging installs the default slog logger at the requested verbosity.
func setupLogging(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
