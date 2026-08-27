// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

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
	cmd.AddCommand(newImageListCmd(), newImageExtractCmd(), newImageManifestCmd())
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
			paths, err := walkFiles(b)
			if err != nil {
				return err
			}
			// Settled once for the same reason the walk is: deciding whether the WIM hint
			// applies reads the image's root, and the answer cannot differ between names.
			// Calling the image's own resolver per name would repeat that read each time.
			l := imageListing(b)
			type target struct{ name, from, dest string }
			var targets []target
			claimed := make(map[string]string, len(names))
			for _, name := range names {
				from, err := resolveName(b, paths, name, l)
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
		merged, err := wimLayoutBacking(b)
		if err == nil {
			return merged, nil
		}
		// The fallback is deliberate — the media-root files stay readable — but the reason
		// the payload could not be read is the only thing that distinguishes a truncated
		// WIM from one this reader does not understand, and it is lost here otherwise. The
		// callers report the failure without it, so -v is what recovers the cause.
		slog.Debug("wim payload unreadable, reading media root only",
			"image", target, "wim", wimImageFile, "err", err)
	}
	return b, nil
}

// imageListing is how the shared file helpers should talk about a built image.
//
// The hint is computed per call rather than fixed, because it applies only to a
// wim-layout image whose payload is sealed inside PE.wim: there the listing shows the
// media root alone (the WIM, the boot core, the loader) and a bare result would read as
// "that is all there is".
func imageListing(b source.Backing) listing {
	l := listing{noun: "image"}
	if wimPayloadOpaque(b) {
		l.hint = wimImageHint
	}
	return l
}

func printImageList(w io.Writer, b source.Backing, pattern string, long bool) error {
	return printFileList(w, b, pattern, long, imageListing(b))
}

// resolveImageName resolves a name to exactly one path in the image, against a file
// list the caller has already walked.
func resolveImageName(b source.Backing, paths []string, name string) (string, error) {
	return resolveName(b, paths, name, imageListing(b))
}

// readImageFile resolves a name and reads it, walking the image itself. Callers
// resolving several names should walk once and use resolveImageName directly.
func readImageFile(b source.Backing, name string) (data []byte, from string, err error) {
	paths, err := walkFiles(b)
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
