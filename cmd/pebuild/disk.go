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
	"strings"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/internal/disk"
)

// diskListing is how the shared file helpers talk about a scratch disk. There is no
// hint: a disk holds what was put on it and what a guest wrote, so an empty listing is
// the whole answer rather than a misleading half of one.
var diskListing = listing{noun: "disk"}

// newDiskCmd groups the commands that make and read a scratch disk — the storage a PE is
// booted with when the question is how it behaves with a disk attached.
//
// These are not the `image` commands with a different backing. An image is something a
// build produced and pebuild only ever reads; a disk is something pebuild creates empty
// and both sides write to, which is why this group has verbs that put files in as well as
// take them out. Reading a disk needs no mount and no root, so inspecting what a PE wrote
// does not depend on the host being able to mount FAT.
func newDiskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disk",
		Short: "Make and inspect a scratch disk to boot a PE with",
		Long: "Create, list, fill and read a scratch disk image: a partitioned, formatted\n" +
			"FAT volume that `pebuild run` can attach to a boot. Use these to put a file\n" +
			"where the guest will find it, and to read back what the guest wrote.\n\n" +
			"A disk is read as a plain filesystem, so no mounting and no root is needed.\n" +
			"Any raw image with an MBR and a FAT partition can be read, not only the ones\n" +
			"this tool wrote.",
	}
	cmd.AddCommand(newDiskCreateCmd(), newDiskListCmd(), newDiskAddCmd(), newDiskExtractCmd())
	return cmd
}

func newDiskCreateCmd() *cobra.Command {
	var size, fsName, label string
	cmd := &cobra.Command{
		Use:   "create <path>",
		Short: "Create an empty scratch disk",
		Long: "Create a scratch disk: one MBR partition, formatted FAT and labelled, ready\n" +
			"to attach to a boot.\n\n" +
			"The size is the whole disk; the volume inside it is a megabyte smaller,\n" +
			"which is the partition alignment. Sizes take a K, M or G suffix and are\n" +
			"binary, so 256M is 256 MiB.\n\n" +
			"The filesystem follows the size unless --fs names one: FAT16 while it fits,\n" +
			"FAT32 beyond. An existing file is never overwritten — a disk may hold what a\n" +
			"guest last wrote to it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := disk.ParseSize(size)
			if err != nil {
				return err
			}
			fsType, err := disk.ParseFSType(fsName)
			if err != nil {
				return err
			}
			if err := disk.Create(args[0], disk.Spec{Size: n, FS: fsType, Label: label}); err != nil {
				return err
			}

			// Reopened rather than reported from the request: the volume is smaller than
			// the disk and the filesystem may have been chosen rather than given, so what
			// is printed should come from the disk that now exists.
			img, err := disk.OpenRead(args[0])
			if err != nil {
				return err
			}
			defer img.Close()
			printDiskInfo(cmd.OutOrStdout(), "Created", img.Info())
			return nil
		},
	}
	cmd.Flags().StringVar(&size, "size", "", "disk size, e.g. 256M (required)")
	cmd.Flags().StringVar(&fsName, "fs", "", "filesystem to format: fat16 or fat32 (default: from the size)")
	cmd.Flags().StringVar(&label, "label", "", "volume label (default: "+disk.DefaultLabel+")")
	_ = cmd.MarkFlagRequired("size")
	return cmd
}

func newDiskListCmd() *cobra.Command {
	var long bool
	cmd := &cobra.Command{
		Use:     "ls <disk> [pattern]",
		Aliases: []string{"list"},
		Short:   "List the files on a scratch disk",
		Long: "List the files on a scratch disk. With no pattern, every file is listed; a\n" +
			"pattern matches against both the file's base name and its full path, so\n" +
			"`hello.txt`, `*.txt` and `logs/*.txt` all work.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var pattern string
			if len(args) > 1 {
				pattern = args[1]
			}
			// Listing writes nothing, so it must not take the disk for writing: a disk
			// on read-only media, or one copied out of a build, is still readable.
			img, err := disk.OpenRead(args[0])
			if err != nil {
				return err
			}
			defer img.Close()
			return printFileList(cmd.OutOrStdout(), img.FS(), pattern, long, diskListing)
		},
	}
	cmd.Flags().BoolVarP(&long, "long", "l", false, "show each file's size")
	return cmd
}

func newDiskAddCmd() *cobra.Command {
	var dest string
	var replace bool
	cmd := &cobra.Command{
		Use:   "add <disk> <file>...",
		Short: "Copy a host file onto a scratch disk",
		Long: "Copy one or more files from the host onto a scratch disk, so a guest booted\n" +
			"with it finds them there.\n\n" +
			"Files land in the root of the disk under their own base name, or in the\n" +
			"--dest directory, which is created if it does not exist. An existing file on\n" +
			"the disk is not overwritten unless --replace is given: the guest may have\n" +
			"written it. A name already taken by a directory is refused either way.\n\n" +
			"Every file is read and every destination checked before anything is written,\n" +
			"so a name that cannot be added leaves the disk untouched. Once writing has\n" +
			"begun a failure part way — a full disk, say — leaves the files already\n" +
			"written in place; the one that failed is removed rather than left empty.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			img, err := disk.Open(args[0])
			if err != nil {
				return err
			}
			defer img.Close()

			targets, err := planDiskAdd(img, args[1:], dest, replace)
			if err != nil {
				return err
			}
			for _, t := range targets {
				if err := img.WriteFile(t.to, t.data); err != nil {
					return fmt.Errorf("write %s: %w", t.to, err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s -> %s (%d bytes)\n", t.from, t.to, len(t.data))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dest, "dest", "d", "", "directory on the disk to write into (default: the root)")
	cmd.Flags().BoolVar(&replace, "replace", false, "overwrite a file that already exists on the disk")
	return cmd
}

func newDiskExtractCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "extract <disk> <file>...",
		Short: "Extract a file from a scratch disk",
		Long: "Extract one or more files from a scratch disk, by exact path (logs/boot.txt)\n" +
			"or by bare name when that name is unique on the disk. Files are written into\n" +
			"the --out directory under their lowercased base name; --out - writes a single\n" +
			"file to stdout.\n\n" +
			"Two files that would write to the same name are refused before anything is\n" +
			"written — extract them separately.\n\n" +
			"This is how what a guest wrote is read back, with no mount and no root.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args[1:]
			stdout := out == "-"
			if stdout && len(names) > 1 {
				return fmt.Errorf("--out - writes one file to stdout; %d were named", len(names))
			}
			img, err := disk.OpenRead(args[0])
			if err != nil {
				return err
			}
			defer img.Close()

			// Same discipline as extracting from an image: resolve everything before
			// writing anything, so a name that does not resolve or two that collide leave
			// no half-finished extraction behind, and one walk serves every name.
			b := img.FS()
			paths, err := walkFiles(b)
			if err != nil {
				return err
			}
			type target struct{ name, from, dest string }
			var targets []target
			claimed := make(map[string]string, len(names))
			for _, name := range names {
				from, err := resolveName(b, paths, name, diskListing)
				if err != nil {
					return err
				}
				d, err := outputName(from)
				if err != nil {
					return err
				}
				if prev, ok := claimed[d]; ok {
					return fmt.Errorf("%q and %q both write %q; extract them separately",
						prev, from, d)
				}
				claimed[d] = from
				targets = append(targets, target{name: name, from: from, dest: d})
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

// diskAddTarget is one planned copy: host file, destination path on the disk, contents.
type diskAddTarget struct {
	from string
	to   string
	data []byte
}

// planDiskAdd resolves every host file and destination before any write happens.
//
// Front-loading everything that can fail is what keeps a partial add from happening: a
// disk left with three of five files added, and no record of which, is worse than one
// that refused the whole operation. Reading the contents here is part of that — a file
// that disappears between planning and writing is the one case left, and it is much
// narrower than the whole loop.
func planDiskAdd(img *disk.Image, files []string, dest string, replace bool) ([]diskAddTarget, error) {
	dir, err := normalizeDiskDir(dest)
	if err != nil {
		return nil, err
	}
	targets := make([]diskAddTarget, 0, len(files))
	claimed := make(map[string]string, len(files))

	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory; name the files to add", f)
		}
		base := filepath.Base(f)
		to := path.Join(dir, base)

		// Two host files with the same base name would silently leave only the last one
		// on the disk, which reads as a successful add of both.
		if prev, ok := claimed[strings.ToLower(to)]; ok {
			return nil, fmt.Errorf("%q and %q both write %q; add them separately", prev, f, to)
		}
		claimed[strings.ToLower(to)] = f

		// Asked of the volume the way FAT answers it. fs.Stat compares the name exactly
		// where a write folds case, so a guest's HELLO.TXT would not be seen by a check
		// for hello.txt — and the write would then find it and replace it, destroying the
		// very thing this command exists to read back.
		if existing, ok := img.Lookup(to); ok {
			// A directory is refused whatever --replace says. There is no sense in which
			// overwriting one is what the caller meant, and doing it corrupts the volume.
			if existing.IsDir {
				return nil, fmt.Errorf("%s is a directory on the disk; add into it with -d %s, or choose another name",
					path.Join(dir, existing.Name), strings.TrimPrefix(path.Join(dir, existing.Name), "/"))
			}
			if !replace {
				// Named as the disk spells it, so a case difference is visible rather
				// than looking like the command refused the name that was typed.
				return nil, fmt.Errorf("%s already exists on the disk; pass --replace to overwrite it",
					path.Join(dir, existing.Name))
			}
		}

		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		targets = append(targets, diskAddTarget{from: f, to: to, data: data})
	}
	return targets, nil
}

// normalizeDiskDir turns a --dest into the absolute directory path on the disk that the
// FAT implementation expects, the root being "/". Backslashes are accepted because the
// destination is a path in a Windows volume and that is how a user will type one.
//
// A drive letter is refused rather than accepted literally. Typing one is the natural
// mistake once backslashes are allowed — the destination looks like a Windows path, so a
// user writes the whole Windows path — and taken at face value it makes a directory
// actually named "D:", holding a character a FAT short name cannot carry, in a place
// nobody asked for. The disk has no drive letter of its own to check against: which
// letter it gets is decided by the guest at boot, which is often the very thing being
// tested.
func normalizeDiskDir(dest string) (string, error) {
	d := strings.ReplaceAll(dest, `\`, "/")
	if len(d) >= 2 && d[1] == ':' {
		return "", fmt.Errorf("--dest %q names a drive; give a path inside the disk (%q), since the drive letter is the guest's to assign",
			dest, strings.TrimPrefix(strings.Trim(d[2:], "/"), "/"))
	}
	d = strings.Trim(d, "/")
	if d == "" {
		return "/", nil
	}
	return "/" + d, nil
}

// printDiskInfo reports a disk the way `run` reports one it attached, so the two agree.
// The volume size is what is shown rather than the disk's, being the space a guest
// actually gets.
func printDiskInfo(w io.Writer, verb string, info disk.Info) {
	fmt.Fprintf(w, "%s %s (%s, %s %q)\n",
		verb, info.Path, disk.FormatSize(info.VolumeSize), info.FS, info.Label)
}
