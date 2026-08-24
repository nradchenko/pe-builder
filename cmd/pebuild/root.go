// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/buildinfo"
	"github.com/nradchenko/pe-builder/internal/manifest"
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
		newVerifyCmd(),
		newRunCmd(),
		newSourceCmd(),
		newImageCmd(),
		newHiveCmd(),
		newPluginsCmd(),
	)

	return root
}

// loadManifest resolves, loads, and validates the manifest named by arg, which is
// empty when the command was given no argument — pebuild.hcl in the current
// directory. It returns the manifest and the path it came from, so a caller can
// report where a setting it objects to was read from.
//
// Every command that takes a manifest goes through it, so they agree on what an
// argument means and refuse an invalid manifest identically.
func loadManifest(arg string) (*manifest.Manifest, string, error) {
	path, err := manifest.Resolve(arg)
	if err != nil {
		return nil, "", err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, "", fmt.Errorf("load %s: %w", path, err)
	}
	if err := manifest.Validate(m); err != nil {
		return nil, "", fmt.Errorf("%s is invalid:\n%w", path, err)
	}
	return m, path, nil
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

// quietLogging drops the log level to warnings for the duration of an operation whose
// progress narration is not this command's output, and returns a function restoring the
// previous logger. Warnings and errors still come through; only the running commentary
// is suppressed.
func quietLogging() func() {
	prev := slog.Default()
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})
	slog.SetDefault(slog.New(handler))
	return func() { slog.SetDefault(prev) }
}
