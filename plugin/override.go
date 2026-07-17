// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nradchenko/pe-builder/action"
)

// Inject copies a local file (From) into the output tree at a target path (To).
type Inject struct {
	From string
	To   string
}

// Override is a manifest's per-plugin layer applied on top of a plugin's own actions.
// File paths (Include, Exclude, Inject.To) are output-tree-relative, e.g.
// "i386/system32/foo.dll"; registry paths (RegSet keys, RegDelete) use HKLM\/HKU\
// hive prefixes with the value name as the last backslash component.
type Override struct {
	// Options overrides the plugin's declared [Options] values (option name -> value).
	// They are validated against the plugin's declarations and substituted into its INF
	// values, not added as actions, so they are handled apart from the layers below.
	Options map[string]string
	// Include names Windows-source files to copy in, by output-tree path.
	Include []string
	// Exclude names the plugin's own copy targets to suppress, by resolved output path.
	Exclude []string
	// Inject copies local files into the output tree.
	Inject []Inject
	// RegSet writes registry values (path -> string value).
	RegSet map[string]string
	// RegDelete removes registry values, by path.
	RegDelete []string
}

// empty reports whether the override layer has nothing to apply.
func (o Override) empty() bool {
	return len(o.Include) == 0 && len(o.Exclude) == 0 && len(o.Inject) == 0 &&
		len(o.RegSet) == 0 && len(o.RegDelete) == 0
}

// includeAction builds a copy of a Windows-source file placed at the output-tree path
// p. The file is read from the source by p's base name; p is its literal destination.
func includeAction(p string) action.Action {
	p = normalizeTreePath(p)
	return action.Action{
		Kind: action.CopyFile,
		Src:  path.Base(p),
		Dest: p,
	}
}

// injectAction builds a copy of a local file into the output tree, plus the origin
// key and local directory the caller registers so the tree assembler can read the
// file. From is a local filesystem path (OS-native, so an absolute path is kept
// intact); To is an output-tree path. originKey is unique per plugin+index. The
// source file is checked here so a missing/mistyped path is a clear error naming it,
// rather than a confusing not-found deep in tree assembly.
func injectAction(inj Inject, originKey string) (a action.Action, dir string, err error) {
	if inj.From == "" || inj.To == "" {
		return action.Action{}, "", fmt.Errorf("inject needs both from and to")
	}
	if _, err := os.Stat(inj.From); err != nil {
		return action.Action{}, "", fmt.Errorf("inject source %q: %w", inj.From, err)
	}
	return action.Action{
		Kind:   action.CopyFile,
		Src:    filepath.Base(inj.From),
		Dest:   normalizeTreePath(inj.To),
		Origin: originKey,
	}, filepath.Dir(inj.From), nil
}

// normalizeTreePath converts a manifest path to the tree's forward-slash form and
// trims surrounding separators.
func normalizeTreePath(p string) string {
	return strings.Trim(strings.ReplaceAll(p, `\`, "/"), "/")
}

// regSetActions maps a manifest registry.set map to AddReg actions writing string
// (REG_SZ) values. Keys are sorted so the action order is deterministic.
func regSetActions(set map[string]string) ([]action.Action, error) {
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	out := make([]action.Action, 0, len(paths))
	for _, p := range paths {
		hive, subkey, value, err := parseRegPath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, action.Action{
			Kind:      action.AddReg,
			Hive:      hive,
			Subkey:    subkey,
			ValueName: value,
			RegType:   action.RegSZ,
			Data:      set[p],
			HasValue:  true,
		})
	}
	return out, nil
}

// regDeleteActions maps manifest registry.delete paths to DelReg actions removing the
// named value.
func regDeleteActions(del []string) ([]action.Action, error) {
	out := make([]action.Action, 0, len(del))
	for _, p := range del {
		hive, subkey, value, err := parseRegPath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, action.Action{
			Kind:      action.DelReg,
			Hive:      hive,
			Subkey:    subkey,
			ValueName: value,
		})
	}
	return out, nil
}

// parseRegPath splits a manifest registry path — a backslash path led by a hive
// prefix — into the target hive, the subkey, and the value name (its last
// component). Recognized prefixes: HKLM\SYSTEM and HKLM\SOFTWARE, HKU\.DEFAULT (or
// HKEY_USERS\.DEFAULT), and HKCU/HKEY_CURRENT_USER (the default-user hive).
func parseRegPath(p string) (hive action.RegHive, subkey, value string, err error) {
	parts := strings.Split(strings.Trim(strings.ReplaceAll(p, "/", `\`), `\`), `\`)
	for len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", "", "", fmt.Errorf("empty registry path")
	}

	var rest []string
	switch strings.ToUpper(parts[0]) {
	case "HKLM", "HKEY_LOCAL_MACHINE":
		if len(parts) < 2 {
			return "", "", "", fmt.Errorf("registry path %q: HKLM needs a SYSTEM or SOFTWARE root", p)
		}
		switch strings.ToUpper(parts[1]) {
		case "SYSTEM":
			hive = action.HiveSystem
		case "SOFTWARE":
			hive = action.HiveSoftware
		default:
			return "", "", "", fmt.Errorf("registry path %q: unsupported HKLM root %q (want SYSTEM or SOFTWARE)", p, parts[1])
		}
		rest = parts[2:]
	case "HKU", "HKEY_USERS":
		if len(parts) < 2 || strings.ToUpper(parts[1]) != ".DEFAULT" {
			return "", "", "", fmt.Errorf("registry path %q: only HKU\\.DEFAULT is supported", p)
		}
		hive = action.HiveDefault
		rest = parts[2:]
	case "HKCU", "HKEY_CURRENT_USER":
		hive = action.HiveDefault
		rest = parts[1:]
	default:
		return "", "", "", fmt.Errorf("registry path %q: unrecognized hive prefix %q", p, parts[0])
	}

	if len(rest) == 0 {
		return "", "", "", fmt.Errorf("registry path %q: names a hive but no value", p)
	}
	value = rest[len(rest)-1]
	subkey = strings.Join(rest[:len(rest)-1], `\`)
	return hive, subkey, value, nil
}
