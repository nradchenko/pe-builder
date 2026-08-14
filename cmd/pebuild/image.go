// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/source"
)

// newImageCmd groups the commands that read a *built* PE image — one this tool
// produced, or a reference image built by another tool — as opposed to the Windows
// installation source the `source` commands read.
//
// The distinction is not bureaucratic. A source is addressed through its own
// declarations: `source ls` resolves a name against txtsetup.sif/layout.inf and
// reports the storage form and target directory, and `source extract` decompresses
// whichever form the media stores a file in. A built image has none of that — the
// build already resolved, decompressed and placed everything — so those commands
// fail on one before they can read a byte. What is wanted there is the plain
// filesystem, which is what these commands give.
//
// `hive dump` already spans both, and takes the same view: it searches known paths
// in the image rather than probing an identity a built PE cannot answer.
func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Inspect a built PE image (its files, as they were placed)",
		Long: "Read a built PE image — a mastered .iso, an output directory, or a work\n" +
			"directory — as a plain filesystem. Use these when the target is something a\n" +
			"build produced (ours or another tool's); use the `source` commands for a\n" +
			"Windows installation source, which is addressed through its own declarations.",
	}
	cmd.AddCommand(newImageListCmd(), newImageExtractCmd())
	return cmd
}

func newImageListCmd() *cobra.Command {
	var long bool
	cmd := &cobra.Command{
		Use:     "ls <image> [pattern]",
		Aliases: []string{"list"},
		Short:   "List the files in a built PE image",
		Long: "List the files in a built PE image. With no pattern, every file is listed;\n" +
			"a pattern matches against both the file's base name and its full path, so\n" +
			"`startnet.cmd`, `*.cmd` and `i386/system32/*.cmd` all work.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var pattern string
			if len(args) > 1 {
				pattern = args[1]
			}
			b, err := openImage(args[0])
			if err != nil {
				return err
			}
			return printImageList(cmd.OutOrStdout(), b, pattern, long)
		},
	}
	cmd.Flags().BoolVarP(&long, "long", "l", false, "show each file's size")
	return cmd
}

func newImageExtractCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "extract <image> <file>...",
		Short: "Extract a file from a built PE image",
		Long: "Extract one or more files from a built PE image, by exact path\n" +
			"(i386/system32/startnet.cmd) or by bare name when that name is unique in the\n" +
			"image. Files are written into the --out directory under their lowercased base\n" +
			"name, so the same file extracted from a work directory and from the .iso\n" +
			"mastered from it lands under one name; --out - writes a single file to stdout.\n\n" +
			"Two files that would write to the same name are refused before anything is\n" +
			"written — extract them separately.\n\n" +
			"Nothing is decompressed: a built image holds the files as the build placed\n" +
			"them. Use `source extract` for a Windows source, where the stored form varies.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args[1:]
			stdout := out == "-"
			if stdout && len(names) > 1 {
				return fmt.Errorf("--out - writes one file to stdout; %d were named", len(names))
			}
			b, err := openImage(args[0])
			if err != nil {
				return err
			}

			// Resolve everything before writing anything: a name that does not resolve, or
			// two that collide, should leave no half-finished extraction behind. One walk
			// serves every name rather than one walk each.
			paths, err := walkImage(b)
			if err != nil {
				return err
			}
			type target struct{ name, from, dest string }
			var targets []target
			claimed := make(map[string]string, len(names))
			for _, name := range names {
				from, err := resolveImageName(b, paths, name)
				if err != nil {
					return err
				}
				dest, err := outputName(from)
				if err != nil {
					return err
				}
				if prev, ok := claimed[dest]; ok {
					return fmt.Errorf("%q and %q both write %q; extract them separately",
						prev, from, dest)
				}
				claimed[dest] = from
				targets = append(targets, target{name: name, from: from, dest: dest})
			}

			if !stdout {
				if err := os.MkdirAll(out, 0o755); err != nil {
					return err
				}
			}
			for _, t := range targets {
				data, err := fs.ReadFile(b, t.from)
				if err != nil {
					return err
				}
				if stdout {
					if _, err := cmd.OutOrStdout().Write(data); err != nil {
						return err
					}
					continue
				}
				dest := filepath.Join(out, t.dest)
				if err := os.WriteFile(dest, data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s -> %s (%d bytes, from %s)\n",
					t.name, dest, len(data), t.from)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", ".", "directory to write into, or - for stdout")
	return cmd
}

// openImage opens a built image for reading. It deliberately does not probe an
// identity or read a layout: a built PE keeps the source's folder names but not the
// files Probe reads, so probing would fail on exactly the targets this command exists
// for. source.Open supplies the case-insensitive backing, which matters because a
// mastered ISO uppercases what the tree wrote in mixed case.
func openImage(target string) (source.Backing, error) {
	src, err := source.Open(target)
	if err != nil {
		return nil, err
	}
	b := src.Backing()
	// A wim-layout image keeps its payload inside PE.wim; present it merged over the media root
	// so the inspection commands read through it transparently. If the WIM cannot be read, fall
	// back to the media-only backing — the guardrail then points the caller at PE.wim.
	if isWIMLayoutImage(b) {
		if merged, err := wimLayoutBacking(b); err == nil {
			return merged, nil
		}
	}
	return b, nil
}

// walkImage collects every file path in the image, in sorted order.
func walkImage(b source.Backing) ([]string, error) {
	var out []string
	err := fs.WalkDir(b, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// normalizeImagePattern validates a caller's pattern once and returns the form
// imageMatch expects: separators unified and lowercased. An empty pattern means match
// everything.
//
// Validating up front is what makes a typo distinguishable from an empty image: a
// malformed glob (`foo[`) would otherwise be swallowed by path.Match's error and
// reported as "0 file(s) matched", which reads as a confident answer.
func normalizeImagePattern(pattern string) (string, error) {
	if pattern == "" {
		return "", nil
	}
	pat := strings.ToLower(strings.ReplaceAll(pattern, `\`, "/"))
	if _, err := path.Match(pat, "probe"); err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	return pat, nil
}

// imageMatch reports whether a path satisfies a normalized pattern, which may name the
// base name or the whole path, as a literal or a glob. Matching is case-insensitive
// because an image's own case is not stable: the tree writes mixed case and the ISO
// stage uppercases, so requiring the caller to know which one they have would make
// the same command work on a work directory and fail on the ISO built from it.
//
// The pattern arrives already normalized (see normalizeImagePattern) so it is not
// re-lowercased for every path in the image.
func imageMatch(p, pat string) bool {
	if pat == "" {
		return true
	}
	lp := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	if ok, _ := path.Match(pat, lp); ok {
		return true
	}
	if ok, _ := path.Match(pat, base); ok {
		return true
	}
	return lp == pat || base == pat
}

func printImageList(w io.Writer, b source.Backing, pattern string, long bool) error {
	pat, err := normalizeImagePattern(pattern)
	if err != nil {
		return err
	}
	paths, err := walkImage(b)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	n := 0
	for _, p := range paths {
		if !imageMatch(p, pat) {
			continue
		}
		n++
		if !long {
			fmt.Fprintln(tw, p)
			continue
		}
		// A size that cannot be read is reported as unknown rather than as a number:
		// printing -1 would read as data.
		size := "?"
		if fi, err := fs.Stat(b, p); err == nil {
			size = strconv.FormatInt(fi.Size(), 10)
		}
		fmt.Fprintf(tw, "%s\t%s\n", p, size)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%d file(s) matched.\n", n)
	// On a wim-layout image the listing shows only the media root (the WIM, the boot core,
	// the loader) — the Windows payload is inside the WIM. Say so when the caller listed
	// everything or matched nothing, the two cases where the empty-looking result would
	// otherwise read as "that is all there is".
	if (pat == "" || n == 0) && wimPayloadOpaque(b) {
		fmt.Fprintf(w, "\nNote: %s.\n", wimImageHint)
	}
	return nil
}

// resolveImageName resolves a name to exactly one path in the image, against a file
// list the caller has already walked. An exact path is tried first; failing that the
// name is matched as a bare name across the image. An ambiguous bare name is an error
// naming the candidates rather than a silent pick, since choosing one would quietly
// answer a different question than the one asked.
func resolveImageName(b source.Backing, paths []string, name string) (string, error) {
	p := strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "/")
	if fi, err := fs.Stat(b, p); err == nil && !fi.IsDir() {
		return p, nil
	}

	var hits []string
	for _, c := range paths {
		if strings.EqualFold(path.Base(c), path.Base(p)) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 0:
		if wimPayloadOpaque(b) {
			return "", fmt.Errorf("no file %q in the image; %s", name, wimImageHint)
		}
		return "", fmt.Errorf("no file %q in the image", name)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q is ambiguous in this image (%s); name the exact path",
			name, strings.Join(hits, ", "))
	}
}

// readImageFile resolves a name and reads it, walking the image itself. Callers
// resolving several names should walk once and use resolveImageName directly.
func readImageFile(b source.Backing, name string) (data []byte, from string, err error) {
	paths, err := walkImage(b)
	if err != nil {
		return nil, "", err
	}
	from, err = resolveImageName(b, paths, name)
	if err != nil {
		return nil, "", err
	}
	data, err = fs.ReadFile(b, from)
	return data, from, err
}
