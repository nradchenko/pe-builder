// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/build"
	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/receipt"
	"github.com/nradchenko/pe-builder/source"
)

func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <image> [manifest.hcl]",
		Short: "Check whether an image was built by this pebuild from a manifest",
		Long: "Read the build receipt an image carries and recompute every field it records\n" +
			"from the current pebuild, manifest and plugin directories, reporting which of\n" +
			"engine, manifest, source, plugin set or options differs.\n\n" +
			"<image> is a mastered .iso or a built file tree. The manifest is the second\n" +
			"argument, else pebuild.hcl in the current directory; with no manifest to be\n" +
			"found the receipt is simply printed.\n\n" +
			"Exits non-zero when the image does not match, or carries no receipt at all.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rec, err := readReceipt(args[0])
			if err != nil {
				return err
			}

			var arg string
			if len(args) == 2 {
				arg = args[1]
			}
			// Show mode is for having *no* manifest, and that is the only thing it may
			// stand in for. Resolve is asked first and separately, because it is the only
			// step that answers "is there a manifest here at all": a manifest that exists
			// but does not load or validate must be an error, never a silent fall back to
			// printing the receipt — that would exit 0 having verified nothing, which is
			// precisely the false confidence this command exists to remove.
			path, err := manifest.Resolve(arg)
			if err != nil {
				return showReceipt(cmd.OutOrStdout(), rec)
			}
			m, path, err := loadManifest(path)
			if err != nil {
				return err
			}

			// Recomputing resolves the plugin set, which narrates what it auto-enables.
			// That is the build's story, not this one's: verify is a report read for its
			// table and its exit code, so the recompute runs quiet unless asked.
			if verbose, _ := cmd.Flags().GetBool("verbose"); !verbose {
				restore := quietLogging()
				defer restore()
			}

			v, err := build.Verify(m, rec)
			if err != nil {
				return err
			}
			printVerification(cmd.OutOrStdout(), v, path)
			if !v.Match() {
				return errSilent
			}
			return nil
		},
	}
	return cmd
}

// errSilent asks for a non-zero exit without a further message. A command returns it
// when it has already printed its own verdict in full, so the exit code carries the
// outcome to a script and the terminal is not told the same thing twice.
var errSilent = errors.New("silent failure")

// readReceipt reads the receipt out of a mastered image or a built tree. Both are opened
// the same way `hive dump` reads a built PE, so an ISO and a directory behave alike.
func readReceipt(image string) (*receipt.Receipt, error) {
	src, err := source.Open(image)
	if err != nil {
		return nil, err
	}
	rec, err := receipt.ReadFS(src.Backing())
	if errors.Is(err, receipt.ErrNotFound) {
		// Deliberately not phrased as a mismatch: an image with no receipt was built
		// before receipts existed, or was not built by pebuild at all, and calling that
		// "does not match" would assert something that was never checked.
		return nil, fmt.Errorf("%s carries no build receipt: it was built before receipts existed, or not by pebuild", image)
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// showReceipt prints what an image records, for when there is no manifest to check it
// against.
func showReceipt(w io.Writer, rec *receipt.Receipt) error {
	data, err := rec.Marshal()
	if err != nil {
		return err
	}
	// The stored form is CRLF, for the environment the image ships to; a terminal here
	// wants plain lines.
	_, err = io.WriteString(w, strings.ReplaceAll(string(data), "\r\n", "\n"))
	return err
}

// printVerification renders the per-field report.
func printVerification(w io.Writer, v *build.Verification, manifestPath string) {
	fmt.Fprintf(w, "Built    %s from %s\n", v.Recorded.BuiltAt.Format("2006-01-02 15:04:05 MST"), v.Recorded.Manifest.File)
	fmt.Fprintf(w, "Checked  against %s\n\n", manifestPath)

	// Rendered into a buffer so the padding the last column would leave on an
	// ungraded row can be trimmed away rather than shipped as trailing spaces.
	var table strings.Builder
	tw := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	for _, f := range v.Fields {
		status := f.Status.String()
		if f.Informational {
			status = ""
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Name, f.Value, status)
	}
	tw.Flush()
	for _, line := range strings.Split(strings.TrimRight(table.String(), "\n"), "\n") {
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}

	for _, f := range v.Fields {
		// What the image claims, for a field that differs and has no per-item detail of
		// its own (engine content, manifest — both single opaque digests). Without it the
		// report says only that the value is not the current one, which cannot be acted
		// on: the recorded digest is what identifies the build that did produce it.
		if f.Status == build.Changed && len(f.Detail) == 0 && f.Recorded != "" {
			fmt.Fprintf(w, "  %s: built with %s\n", f.Name, f.Recorded)
		}
		for _, d := range f.Detail {
			fmt.Fprintf(w, "  %s: %s\n", f.Name, d)
		}
	}

	fmt.Fprintln(w)
	if v.Recorded.Tainted {
		// Worth stating whether or not the image matches: it is the first thing a
		// provenance question wants, and it describes the build that happened rather
		// than the comparison.
		fmt.Fprintf(w, "Tainted: %s\n", v.Recorded.TaintLine())
	}
	switch {
	case v.Match():
		fmt.Fprintln(w, "MATCH: this image was built by this pebuild from this manifest.")
	case anyUnknown(v):
		fmt.Fprintln(w, "INCOMPLETE: some inputs could not be checked; see UNKNOWN above.")
	default:
		fmt.Fprintln(w, "MISMATCH: the image was not built by this pebuild from this manifest.")
	}
}

// anyUnknown reports whether a non-match is (only) because something could not be
// checked, so the summary does not accuse an image of differing when it was never
// compared.
func anyUnknown(v *build.Verification) bool {
	for _, f := range v.Fields {
		if !f.Informational && f.Status == build.Changed {
			return false
		}
	}
	return true
}
