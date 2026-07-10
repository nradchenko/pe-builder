// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/buildinfo"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/source"
)

// Execute runs the root command against os.Args.
func Execute() error {
	return newRootCmd().Execute()
}

// newRootCmd builds the pewalk command. It is a constructor so tests can drive an
// isolated instance.
func newRootCmd() *cobra.Command {
	var (
		isoPath    string
		dirPath    string
		format     string
		missingOnE bool
		maxDepth   int
	)

	cmd := &cobra.Command{
		Use:   "pewalk [flags] <seed>...",
		Short: "Compute the PE import closure of a Windows binary against a source",
		Long: "pewalk is an \"ldd for PE\": it computes the transitive import closure of a\n" +
			"Windows binary against an installation source — a directory or ISO,\n" +
			"including compressed *.??_ files and cabinet-packed (DRIVER.CAB) modules — and\n" +
			"renders it as text, Graphviz DOT, or JSON.\n\n" +
			"Each seed is a PE module name or path, resolved within the source by its bare\n" +
			"file name (case-insensitively). A module absent from the source is reported as\n" +
			"Missing (a red herring), never fatal.\n\n" +
			"The closure is DLL-granular: it records which modules a binary imports (static\n" +
			"and delay-load), not which functions — a resolved module does not imply every\n" +
			"symbol it exports is present.\n\n" +
			"A full closure is large and densely connected, so `--format dot | dot` alone\n" +
			"produces an unreadable canvas. For a legible graph, bound it with --max-depth or\n" +
			"reduce it with Graphviz's `tred` first; the text and json formats scale freely.\n\n" +
			"Examples:\n" +
			"  pewalk --dir ./i386 explorer.exe\n" +
			"  pewalk --iso win.iso --max-depth 2 explorer.exe --format dot | dot -Tsvg -o deps.svg\n" +
			"  pewalk --iso win.iso explorer.exe --format dot | tred | dot -Tsvg -o deps.svg\n" +
			"  pewalk --iso win.iso --missing-only ntoskrnl.exe win32k.sys",
		Args:          cobra.MinimumNArgs(1),
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "text", "dot", "json":
			default:
				return fmt.Errorf("unknown --format %q (want text, dot, or json)", format)
			}
			if isoPath != "" && dirPath != "" {
				return fmt.Errorf("--iso and --dir are mutually exclusive")
			}

			srcPath := isoPath
			if srcPath == "" {
				srcPath = dirPath
			}
			if srcPath == "" {
				// Default to the first seed's own directory, or the current
				// directory when the seeds are bare names.
				if d := filepath.Dir(args[0]); d != "." && d != "" {
					srcPath = d
				} else {
					srcPath = "."
				}
			}

			src, err := source.Open(srcPath)
			if err != nil {
				return err
			}
			pw, err := pewalk.Open(src.Backing())
			if err != nil {
				return fmt.Errorf("index source %s: %w", srcPath, err)
			}
			// Indexing warnings (e.g. a skipped cabinet) go to stderr so they
			// never contaminate the DOT/JSON on stdout.
			for _, warn := range pw.Warnings() {
				fmt.Fprintln(cmd.ErrOrStderr(), "pewalk: warning:", warn)
			}

			res, err := pewalk.WalkWith(pw, pewalk.Options{MaxDepth: maxDepth}, args...)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), res, format, missingOnE)
		},
	}

	cmd.SetVersionTemplate(buildinfo.String("pewalk") + "\n")
	f := cmd.Flags()
	f.StringVar(&isoPath, "iso", "", "resolve within this ISO source")
	f.StringVar(&dirPath, "dir", "", "resolve within this directory source (default: the seed's own dir)")
	f.StringVar(&format, "format", "text", "output format: text, dot, or json")
	f.BoolVar(&missingOnE, "missing-only", false, "show only unresolved modules (text format)")
	f.IntVar(&maxDepth, "max-depth", 0, "bound recursion depth (0 = unbounded)")
	return cmd
}

// render writes the result in the requested format.
func render(w io.Writer, res *pewalk.Result, format string, missingOnly bool) error {
	switch format {
	case "dot":
		res.WriteDOT(w)
	case "json":
		return res.WriteJSON(w)
	default:
		res.WriteText(w, missingOnly)
	}
	return nil
}
