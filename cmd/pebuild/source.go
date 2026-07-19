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

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/compress"
	"github.com/nradchenko/pe-builder/inf"
	"github.com/nradchenko/pe-builder/layout"
	"github.com/nradchenko/pe-builder/pewalk"
	"github.com/nradchenko/pe-builder/source"
	"github.com/nradchenko/pe-builder/tree"
)

func newSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Inspect a Windows installation source",
	}
	cmd.AddCommand(
		newSourceProbeCmd(),
		newSourceExtractCmd(),
		newSourceListCmd(),
		newSourceDirsCmd(),
		newSourceINFCmd(),
	)
	return cmd
}

func newSourceProbeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "probe <path>",
		Short: "Detect a source's identity (build, arch, flavor)",
		Long: "Probe a Windows installation source (a directory or an .iso) and report\n" +
			"its detected identity: build number, architecture, and flavor.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			id, err := src.Probe()
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Build:  %d\nArch:   %s\nFlavor: %s\n", id.Build, id.Arch, id.Flavor)
			return err
		},
	}
}

func newSourceExtractCmd() *cobra.Command {
	var (
		out string
		raw bool
	)
	cmd := &cobra.Command{
		Use:   "extract <source> <file>...",
		Short: "Extract a file from a source, decompressing it",
		Long: "Extract one or more files from a Windows installation source (a directory or\n" +
			"an .iso), decompressing them on the way out. A file is named either by its bare\n" +
			"name (ntoskrnl.exe), resolved against every form the media stores it in — a loose\n" +
			"file, a compressed *.??_ archive, or a member of DRIVER.CAB/SP*.CAB — or by its\n" +
			"exact path in the source (i386/lang/foo.dll) when a bare name is ambiguous.\n\n" +
			"Files are written into the --out directory under their canonical (decompressed)\n" +
			"name; --out - writes a single file to stdout instead.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args[1:]
			stdout := out == "-"
			if stdout && len(names) > 1 {
				return fmt.Errorf("--out - writes one file to stdout; %d were named", len(names))
			}

			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			idx, err := openSourceIndex(cmd.ErrOrStderr(), src)
			if err != nil {
				return err
			}

			if !stdout {
				if err := os.MkdirAll(out, 0o755); err != nil {
					return err
				}
			}
			for _, name := range names {
				data, canonical, origin, err := extractFile(src.Backing(), idx, name, raw)
				if err != nil {
					return err
				}
				if stdout {
					if _, err := cmd.OutOrStdout().Write(data); err != nil {
						return err
					}
					continue
				}
				dest := filepath.Join(out, canonical)
				if err := os.WriteFile(dest, data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s -> %s (%d bytes, from %s)\n",
					name, dest, len(data), origin)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", ".", "directory to write into, or - for stdout")
	cmd.Flags().BoolVar(&raw, "raw", false, "write the stored bytes verbatim, without decompressing")
	return cmd
}

// openSourceIndex indexes a source for name-based resolution, reporting any cabinet
// the index had to skip on w — a skipped cabinet's members resolve as absent, which
// would otherwise look like the media simply not carrying the file.
func openSourceIndex(w io.Writer, src source.Source) (*pewalk.Source, error) {
	idx, err := pewalk.Open(src.Backing())
	if err != nil {
		return nil, err
	}
	for _, warn := range idx.Warnings() {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
	return idx, nil
}

// extractFile resolves one file of an extract request to its bytes, the name to write
// them under, and a description of where they came from. A name carrying a separator
// is an exact source path, read as-is; a bare name goes through the index, which
// resolves it against the loose, compressed-loose, and cabinet-member forms alike.
func extractFile(b source.Backing, idx *pewalk.Source, name string, raw bool) (data []byte, canonical, origin string, err error) {
	if strings.ContainsAny(name, `/\`) {
		return extractPath(b, name, raw)
	}
	if raw {
		return extractRaw(b, idx, name)
	}
	data, o, err := idx.Read(name)
	if err != nil {
		return nil, "", "", err
	}
	return data, strings.ToLower(name), o.String(), nil
}

// extractRaw reads a bare name's stored bytes without decompressing. The index
// locates the file and the bytes are read from that path verbatim, so a loose file
// comes out as itself and a compressed archive comes out as the archive. The name
// written is the stored one, not the canonical one: the raw bytes of NTOSKRNL.EX_
// are a cabinet, and calling them ntoskrnl.exe would misrepresent what they are.
//
// A cabinet member is the one form with no stored file of its own — its bytes exist
// only as part of the cabinet's compressed stream — so it has no raw form to read.
func extractRaw(b source.Backing, idx *pewalk.Source, name string) (data []byte, canonical, origin string, err error) {
	o, ok := idx.Locate(name)
	if !ok {
		return nil, "", "", fmt.Errorf("resolve %q: %w", name, fs.ErrNotExist)
	}
	if o.Kind == pewalk.OriginCab {
		return nil, "", "", fmt.Errorf("%s is packed inside %s and has no stored form of its own; extract it without --raw", name, o.Path)
	}
	data, err = fs.ReadFile(b, o.Path)
	if err != nil {
		return nil, "", "", err
	}
	canonical, err = outputName(o.Path)
	if err != nil {
		return nil, "", "", err
	}
	return data, canonical, o.String(), nil
}

// outputName reduces a source path or an archive member name to the bare filename it
// is written under. Both can carry directory components — a member name is a raw
// string out of the cabinet's file table, and the cabinet format allows a path there —
// so taking the base is what keeps an extraction inside the requested directory
// rather than somewhere a crafted name chose. Backslashes count as separators: that
// is what a cabinet spells its paths with.
func outputName(name string) (string, error) {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	if base == "." || base == ".." || base == "/" || base == "" {
		return "", fmt.Errorf("%q does not name a file that can be written", name)
	}
	return strings.ToLower(base), nil
}

// extractPath reads a file addressed by its exact path in the source. A stored file
// may be a single-file *.??_ archive, whose real name lives in the archive's own
// directory — so an extracted ntoskrnl.ex_ is written as ntoskrnl.exe rather than
// under a name guessed from the truncated extension.
func extractPath(b source.Backing, p string, raw bool) (data []byte, canonical, origin string, err error) {
	p = strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "/")
	data, err = fs.ReadFile(b, p)
	if err != nil {
		return nil, "", "", err
	}
	canonical, err = outputName(p)
	if err != nil {
		return nil, "", "", err
	}
	if raw || !compress.IsCabinet(data) {
		return data, canonical, p, nil
	}

	cab, err := compress.ReadCabinet(data)
	if err != nil {
		return nil, "", "", fmt.Errorf("read cabinet %s: %w", p, err)
	}
	files := cab.Files()
	if len(files) != 1 {
		return nil, "", "", fmt.Errorf("%s holds %d files; name the member to extract instead of the cabinet", p, len(files))
	}
	plain, err := cab.Data(files[0])
	if err != nil {
		return nil, "", "", fmt.Errorf("extract %s from %s: %w", files[0].Name, p, err)
	}
	// The member's own name, which the archive carries, is the file's real name —
	// it cannot be recovered from the truncated *.??_ extension. It is also a raw
	// string out of the archive, so it is reduced to a bare filename before use.
	canonical, err = outputName(files[0].Name)
	if err != nil {
		return nil, "", "", err
	}
	return plain, canonical, p + " (compressed)", nil
}

func newSourceListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "ls <source> [pattern]",
		Aliases: []string{"list"},
		Short:   "List a source's files, how each is stored, and where it would land",
		Long: "List the files a Windows installation source declares, matching an optional\n" +
			"shell-style pattern (case-insensitive, e.g. 'kbd*'). For each: the form the media\n" +
			"stores it in — loose, a compressed *.??_ archive, or a member of a driver cabinet —\n" +
			"and the output-tree path a build would place it at.\n\n" +
			"Only files the source's own metadata declares are listed by default; --all also\n" +
			"lists everything else present on the media, which has no declared placement.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern := "*"
			if len(args) == 2 {
				pattern = strings.ToLower(args[1])
			}

			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			id, err := src.Probe()
			if err != nil {
				return err
			}
			lay, err := layout.Parse(src.Backing(), id)
			if err != nil {
				return err
			}
			idx, err := openSourceIndex(cmd.ErrOrStderr(), src)
			if err != nil {
				return err
			}
			return printSourceList(cmd.OutOrStdout(), lay, id.Roots(), idx, pattern, all)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "also list files the source's metadata does not declare")
	return cmd
}

// printSourceList renders the matching declared files, then — with all set — the
// matching files that are on the media but absent from its metadata. An undeclared
// file has no target: nothing places it, so it is shown with a "-" rather than the
// system32 fallback that resolution would fall back to if a plugin named it.
func printSourceList(w io.Writer, lay *layout.Layout, roots source.Roots, idx *pewalk.Source, pattern string, all bool) error {
	names := make([]string, 0, len(lay.Files))
	for name := range lay.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTORED AS\tTARGET")
	declared := 0
	for _, name := range names {
		match, err := path.Match(pattern, name)
		if err != nil {
			return fmt.Errorf("bad pattern %q: %w", pattern, err)
		}
		if !match {
			continue
		}
		declared++
		stored := "not on media"
		if o, found := idx.Locate(name); found {
			stored = o.String()
		}
		target := tree.OutputPath(action.Action{
			Kind:  action.CopyFile,
			Src:   name,
			DirID: lay.Files[name].TargetDir,
		}, lay, roots)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, stored, target)
	}

	undeclared := 0
	if all {
		for _, e := range idx.Index() {
			if _, ok := lay.Files[e.Name]; ok {
				continue
			}
			match, err := path.Match(pattern, e.Name)
			if err != nil {
				return fmt.Errorf("bad pattern %q: %w", pattern, err)
			}
			if !match {
				continue
			}
			undeclared++
			fmt.Fprintf(tw, "%s\t%s\t-\n", e.Name, e.Origin)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n%d declared", declared)
	if all {
		fmt.Fprintf(w, ", %d undeclared", undeclared)
	}
	fmt.Fprintln(w, " file(s) matched.")
	return nil
}

func newSourceDirsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dirs <source>",
		Short: "Print the source's directory-ID table",
		Long: "Print the [WinntDirectories] table from the source's txtsetup.sif: the\n" +
			"directory ID a file's placement refers to, the path it maps to relative to the\n" +
			"system root, and the output-tree path a build writes that directory under.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			id, err := src.Probe()
			if err != nil {
				return err
			}
			lay, err := layout.Parse(src.Backing(), id)
			if err != nil {
				return err
			}
			printSourceDirs(cmd.OutOrStdout(), lay, id.Roots())
			return nil
		},
	}
}

func printSourceDirs(w io.Writer, lay *layout.Layout, roots source.Roots) {
	ids := make([]string, 0, len(lay.Dirs))
	for id := range lay.Dirs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return dirIDLess(ids[i], ids[j]) })

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPATH\tTARGET")
	for _, id := range ids {
		rel := lay.Dirs[id]
		shown := rel
		if shown == "" {
			// The system root maps to the empty relative path; show it as the
			// backslash the table itself spells it with.
			shown = `\`
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", id, shown, path.Join(roots.For(source.AreaSystem), rel))
	}
	tw.Flush()
}

// dirIDLess orders two directory IDs. The IDs are numeric, so they sort by value:
// a lexical sort would file 10 between 1 and 2 and scatter the table. A non-numeric
// ID (which a source is not expected to carry) sorts after the numbers, lexically.
func dirIDLess(a, b string) bool {
	na, aErr := strconv.Atoi(a)
	nb, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return na < nb
	case aErr == nil:
		return true
	case bErr == nil:
		return false
	default:
		return a < b
	}
}

func newSourceINFCmd() *cobra.Command {
	var (
		build int
		raw   bool
	)
	cmd := &cobra.Command{
		Use:   "inf <source> <file> [section]",
		Short: "Dump a section of an INF from the source",
		Long: "Parse an INF off a Windows installation source — txtsetup.sif, layout.inf,\n" +
			"hivesys.inf, a driver INF — with the same parser the build uses, and print one\n" +
			"section. Without a section name, list the sections the file declares.\n\n" +
			"The file is named the same way as for extract: a bare name, or an exact source\n" +
			"path. Values print as the engine sees them — %TOKEN% substituted from [Strings],\n" +
			"continuation lines joined, quotes stripped — unless --raw prints them as written.\n" +
			"Sections conditioned on a build number resolve against the source's own build\n" +
			"unless --build overrides it.",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Open(args[0])
			if err != nil {
				return err
			}
			if build == 0 {
				// A tree that is not a full source (a build's output) still holds INFs
				// worth reading, so a failed probe is not fatal: build 0 just selects
				// no .BuildNo-conditioned section.
				if id, err := src.Probe(); err == nil {
					build = id.Build
				}
			}
			idx, err := openSourceIndex(cmd.ErrOrStderr(), src)
			if err != nil {
				return err
			}
			data, _, _, err := extractFile(src.Backing(), idx, args[1], false)
			if err != nil {
				return err
			}
			f, err := inf.Parse(data)
			if err != nil {
				return fmt.Errorf("parse %s: %w", args[1], err)
			}

			w := cmd.OutOrStdout()
			if len(args) == 2 {
				for _, s := range f.Sections() {
					fmt.Fprintf(w, "[%s]\n", s)
				}
				return nil
			}
			return printINFSection(w, f, args[2], build, raw)
		},
	}
	cmd.Flags().IntVar(&build, "build", 0, "build number for .BuildNo sections (default: the source's own)")
	cmd.Flags().BoolVar(&raw, "raw", false, "print lines as written, without %TOKEN% expansion")
	return cmd
}

func printINFSection(w io.Writer, f *inf.File, name string, build int, raw bool) error {
	// Accept the bracketed spelling too, so a section can be pasted straight from a
	// listing or from the INF itself.
	name = strings.Trim(name, "[]")
	// A section can exist only in its .BuildNo form, which HasSection (base name
	// only) does not see, so presence is either a base section — possibly an empty
	// one, which is still a section — or lines contributed by the build's suffix.
	lines := f.Section(name, build)
	if !f.HasSection(name) && len(lines) == 0 {
		return fmt.Errorf("no section [%s] in this INF", name)
	}
	fmt.Fprintf(w, "[%s]\n", name)
	for _, ln := range lines {
		if raw {
			fmt.Fprintln(w, ln.Raw())
			continue
		}
		fields := strings.Join(ln.Fields(), ", ")
		if ln.Key == "" {
			fmt.Fprintln(w, fields)
			continue
		}
		fmt.Fprintf(w, "%s = %s\n", ln.Key, fields)
	}
	return nil
}
