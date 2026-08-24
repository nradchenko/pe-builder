// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/hive"
	"github.com/nradchenko/pe-builder/source"
)

func newHiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hive",
		Short: "Inspect Windows registry hives",
	}
	cmd.AddCommand(newHiveDumpCmd())
	return cmd
}

func newHiveDumpCmd() *cobra.Command {
	var (
		file  string
		depth int
		full  bool
	)
	cmd := &cobra.Command{
		Use:   "dump <target> [path]",
		Short: "Dump a registry key (or one value) from a hive",
		Long: "Read a registry hive and print a key, its values, and its subkeys. The target\n" +
			"is a built PE (an .iso or an output/work directory), a Windows installation\n" +
			"source, or a hive file itself.\n\n" +
			"The registry path selects both the hive and the key within it: its first component\n" +
			"names the hive (SYSTEM, SOFTWARE, DEFAULT, SAM, SECURITY, or SETUPREG for a\n" +
			"source's text-setup hive), which is located in the target automatically. A leading\n" +
			"HKLM\\ or HKEY_LOCAL_MACHINE\\ is accepted and ignored. Use --file to name a hive\n" +
			"inside the target directly, in which case the path addresses a key within it.\n\n" +
			"Naming a value instead of a key prints just that value.",
		Example: "  pebuild hive dump out/pe.iso 'SYSTEM\\ControlSet001\\Services\\atapi'\n" +
			"  pebuild hive dump out/pe.iso 'SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\Winlogon\\Shell'\n" +
			"  pebuild hive dump /path/to/xp.iso 'SETUPREG\\ControlSet001\\Services' --depth 1",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath := ""
			if len(args) == 2 {
				regPath = args[1]
			}
			hiveName, keyPath := splitRegPath(regPath)
			// A regf file is the hive itself, so it needs no hive named to find one.
			// Determined once here and passed down: it costs a file read, and it is
			// what decides how the target is opened at all.
			bare := file == "" && isHiveFile(args[0])
			if file == "" && hiveName == "" && !bare {
				return fmt.Errorf("name the hive to read: a registry path starting with one of %s, or --file",
					strings.Join(hiveNames(), ", "))
			}

			data, from, err := readHiveData(args[0], file, hiveName, bare)
			if err != nil {
				return err
			}
			h, err := hive.ReadHive(data)
			if err != nil {
				return fmt.Errorf("read hive %s: %w", from, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "hive: %s\n", from)

			return dumpHive(cmd.OutOrStdout(), h, keyPath, depth, full)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path of the hive within the target, instead of locating it by name")
	cmd.Flags().IntVar(&depth, "depth", 0, "how many subkey levels to descend (0 = all)")
	cmd.Flags().BoolVar(&full, "full", false, "print binary data in full instead of truncating it")
	return cmd
}

// hiveRelPaths lists, per hive name, where that hive lives relative to the folder
// that holds a target's system area, in probe order. The same relative paths serve
// an output directory, a work directory, and a mastered .iso alike, since a build's
// tree keeps its layout when it is mastered.
//
// SYSTEM is the one that is not where its name suggests. A build writes it to
// system32\setupreg.hiv, because in /minint mode the loader loads setupreg.hiv as the
// SYSTEM hive and never consults config\SYSTEM (see internal/build/hives.go); a Windows
// installation source carries its own skeletal setupreg.hiv at the root of the system
// root. So SYSTEM probes both, and config\SYSTEM last for a target that does have an
// installed-Windows layout. SETUPREG is a spelling of the same hive, kept because
// that is the name the file has on the media.
var hiveRelPaths = map[string][]string{
	"system":   systemHiveRelPaths,
	"setupreg": systemHiveRelPaths,
	"software": {"system32/config/software"},
	"default":  {"system32/config/default"},
	"sam":      {"system32/config/sam"},
	"security": {"system32/config/security"},
}

var systemHiveRelPaths = []string{
	"system32/setupreg.hiv",
	"setupreg.hiv",
	"system32/config/system",
}

// hivePaths expands a hive's relative locations across the folders that can hold
// them, in probe order. A target names its system area either by that folder — an
// i386 source or build, an amd64 one — or not at all, for a tree handed over already
// rooted at the system folder, so the unprefixed form is tried first and each
// architecture folder after it.
//
// The architecture folders come from the source package rather than being written
// out here, so this agrees with the layout a build reads and writes. Naming one
// folder is what made this command unable to find a hive in an amd64 target, whose
// system area is AMD64 rather than I386. It searches instead of resolving a probed
// Identity because a built PE cannot be probed: it keeps the source's folder names,
// but the files Probe reads are decompressed and moved during the build.
func hivePaths(hiveName string) []string {
	rel, ok := hiveRelPaths[hiveName]
	if !ok {
		return nil
	}
	roots := append([]string{""}, source.ArchFolders()...)
	paths := make([]string, 0, len(rel)*len(roots))
	for _, r := range rel {
		for _, root := range roots {
			paths = append(paths, path.Join(root, r))
		}
	}
	return paths
}

// hiveNames are the hive names a registry path may start with, for error reporting.
func hiveNames() []string {
	names := make([]string, 0, len(hiveRelPaths))
	for n := range hiveRelPaths {
		names = append(names, strings.ToUpper(n))
	}
	sort.Strings(names)
	return names
}

// splitRegPath separates a registry path into the hive it names and the key path
// within that hive. A leading HKLM\ (or HKEY_LOCAL_MACHINE\) is dropped: it is how
// these paths are habitually written and quoted, and every hive here is a machine
// hive, so it carries no information. The hive name is empty when the path names no
// known hive — the caller then needs --file, or a target that is a hive itself.
func splitRegPath(p string) (hiveName, keyPath string) {
	p = strings.Trim(strings.ReplaceAll(p, "/", `\`), `\`)
	for _, prefix := range []string{"hkey_local_machine", "hklm"} {
		head, rest, _ := strings.Cut(p, `\`)
		if strings.EqualFold(head, prefix) {
			p = rest
			break
		}
	}
	head, rest, _ := strings.Cut(p, `\`)
	if _, ok := hiveRelPaths[strings.ToLower(head)]; ok {
		return strings.ToLower(head), rest
	}
	return "", p
}

// isHiveFile reports whether path is a regf file on the host filesystem — the regf
// signature, read from the file itself. It is what lets a bare hive file be dumped
// without saying which hive it is: the bytes answer that.
func isHiveFile(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return bytes.Equal(magic[:], []byte("regf"))
}

// readHiveData returns the bytes of the hive to dump and a label for where they came
// from. The target is either a hive file on the host, or a source/build tree (a
// directory or an .iso) in which the hive is found by explicit path or by name.
//
// A bare hive file is read directly whatever the registry path says. Naming the hive
// in the path (SYSTEM\…) is how these paths are habitually written, and it addresses
// the key within the file perfectly well — it must not send a regf file down the
// source-opening branch, which would try to read it as an ISO.
func readHiveData(target, file, hiveName string, bare bool) (data []byte, from string, err error) {
	if bare {
		data, err = os.ReadFile(target)
		return data, target, err
	}

	src, err := source.Open(target)
	if err != nil {
		return nil, "", err
	}
	b := src.Backing()
	// A wim-layout image keeps its hives inside PE.wim; read through it when possible so the
	// hive lookup below finds them, falling back to the media-only backing otherwise.
	if isWIMLayoutImage(b) {
		if merged, err := wimLayoutBacking(b); err == nil {
			b = merged
		}
	}

	if file != "" {
		p := strings.TrimPrefix(strings.ReplaceAll(file, `\`, "/"), "/")
		data, err = fs.ReadFile(b, p)
		if err != nil {
			return nil, "", err
		}
		return data, path.Join(target, p), nil
	}

	// A hive with no known locations is a name this command does not carry, which is
	// a different answer from "not found in this target": reporting it as the latter
	// would print an empty search list, reading as though nowhere was looked.
	paths := hivePaths(hiveName)
	if len(paths) == 0 {
		return nil, "", fmt.Errorf("unknown hive %s: name one of %s, or use --file",
			strings.ToUpper(hiveName), strings.Join(hiveNames(), ", "))
	}

	var tried []string
	for _, p := range paths {
		if data, err := fs.ReadFile(b, p); err == nil {
			return data, path.Join(target, p), nil
		}
		tried = append(tried, p)
	}
	if wimPayloadOpaque(b) {
		return nil, "", fmt.Errorf("no %s hive in %s (looked in %s); %s",
			strings.ToUpper(hiveName), target, strings.Join(tried, ", "), wimImageHint)
	}
	return nil, "", fmt.Errorf("no %s hive in %s (looked in %s)",
		strings.ToUpper(hiveName), target, strings.Join(tried, ", "))
}

// dumpHive prints the key at keyPath, or — when keyPath's last component names a
// value rather than a subkey — that single value. A key is looked up first, so a key
// and a value of the same name under one parent resolve to the key, matching how the
// path would be read as an address.
func dumpHive(w io.Writer, h *hive.Hive, keyPath string, depth int, full bool) error {
	keyPath = strings.Trim(strings.ReplaceAll(keyPath, "/", `\`), `\`)
	if keyPath == "" {
		printKey(w, h.Root, h.Root.Name, depth, full)
		return nil
	}

	if k, ok := h.Root.FindPath(keyPath); ok {
		printKey(w, k, h.Root.Name+`\`+keyPath, depth, full)
		return nil
	}

	// Not a key, so the last component may name a value in the key above it. A path
	// with no separator at all names a value directly under the hive root, which is
	// the root itself as the parent — not a case with no parent to look in.
	parentPath, valueName, hasParent := cutLast(keyPath)
	parent := h.Root
	if hasParent {
		p, ok := h.Root.FindPath(parentPath)
		if !ok {
			return fmt.Errorf("no key or value at %s", keyPath)
		}
		parent = p
	}
	if v, ok := parent.Value(valueName); ok {
		fmt.Fprintf(w, "%s = %s\n", v.Name, formatValue(v, full))
		return nil
	}
	return fmt.Errorf("no key or value at %s", keyPath)
}

// cutLast splits a registry path into its parent path and last component. It reports
// false when the path has no separator, so the caller does not retry a lookup it has
// already made against the root.
func cutLast(p string) (parent, last string, ok bool) {
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return "", p, false
	}
	return p[:i], p[i+1:], true
}

// printKey renders a key as a section header, its values, then its subkeys, recursing
// until depth levels have been descended (0 descends all the way). Values come before
// subkeys so a key's own content is not pushed past its children's.
func printKey(w io.Writer, k *hive.Key, keyPath string, depth int, full bool) {
	fmt.Fprintf(w, "\n[%s]\n", keyPath)

	values := append([]hive.Value(nil), k.Values...)
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	for _, v := range values {
		name := v.Name
		if name == "" {
			// The default value has no name; "@" is how the registry's own export
			// format spells it.
			name = "@"
		}
		fmt.Fprintf(w, "    %s = %s\n", name, formatValue(v, full))
	}

	if depth == 1 {
		if n := len(k.Subkeys); n > 0 {
			fmt.Fprintf(w, "    (%d subkey(s) not shown)\n", n)
		}
		return
	}
	subkeys := append([]*hive.Key(nil), k.Subkeys...)
	sort.Slice(subkeys, func(i, j int) bool { return subkeys[i].Name < subkeys[j].Name })
	for _, sub := range subkeys {
		printKey(w, sub, keyPath+`\`+sub.Name, depth-1, full)
	}
}

// maxBinaryBytes is how much of a binary value is shown before it is truncated. It is
// enough to recognize a value at a glance (a GUID, a small struct, a mask) while
// keeping a key holding an icon or a security blob readable; --full prints all of it.
const maxBinaryBytes = 32

// formatValue renders a value's data according to its type, in a form that reads back
// the way the registry's own export format writes it. An unknown type falls back to
// hex, which is always faithful.
func formatValue(v hive.Value, full bool) string {
	switch v.Type {
	case hive.RegSZ:
		return quoteRegSZ(v.AsString())
	case hive.RegExpandSZ:
		return "expand:" + quoteRegSZ(v.AsString())
	case hive.RegDWord:
		d := v.AsDWord()
		return fmt.Sprintf("dword:%08x (%d)", d, d)
	case hive.RegQWord:
		q := v.AsQWord()
		return fmt.Sprintf("qword:%016x (%d)", q, q)
	case hive.RegMultiSZ:
		items := v.AsMultiSZ()
		quoted := make([]string, len(items))
		for i, s := range items {
			quoted[i] = quoteRegSZ(s)
		}
		return "multi:[" + strings.Join(quoted, " ") + "]"
	case hive.RegNone:
		return "none"
	default:
		return formatBinary(v, full)
	}
}

// quoteRegSZ quotes a string value for display. Go's %q would escape every backslash,
// which doubles them through the path- and key-shaped strings that fill these hives
// (system32\drivers\ramdrive.sys) and makes them hard to read or paste back. A string
// with nothing to escape is therefore quoted as-is, and only one holding a control
// character — where the escaping is what carries the information — falls back to %q.
func quoteRegSZ(s string) string {
	if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '"' }) {
		return fmt.Sprintf("%q", s)
	}
	return `"` + s + `"`
}

func formatBinary(v hive.Value, full bool) string {
	data := v.Data
	suffix := ""
	if !full && len(data) > maxBinaryBytes {
		data = data[:maxBinaryBytes]
		suffix = fmt.Sprintf("... (%d bytes)", len(v.Data))
	}
	var sb strings.Builder
	if v.Type != hive.RegBinary {
		// An unrecognized type is still dumped, but say which one it was.
		fmt.Fprintf(&sb, "type%d:", v.Type)
	} else {
		sb.WriteString("hex:")
	}
	for i, b := range data {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%02x", b)
	}
	sb.WriteString(suffix)
	return sb.String()
}
