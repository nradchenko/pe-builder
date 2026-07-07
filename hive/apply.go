// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"fmt"
	"strings"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/inf"
)

// Apply overlays the registry actions in acts onto the base hives, in order, so
// a later write to the same value wins (last-writer-wins). Each AddReg/DelReg
// action is routed to the hive named by its Hive field: HiveSystem → SYSTEM,
// HiveSoftware → SOFTWARE, HiveDefault → the default-user hive. The subkey path
// in each action is relative to that hive's root, exactly as setupapi treats a
// hive mounted under HKLM\SYSTEM, HKLM\SOFTWARE, or HKU\.DEFAULT.
//
// Non-registry actions (file copies, INI/text edits) are ignored here; they are
// executed by the tree and inifile stages.
func (h *Hives) Apply(acts []action.Action) error {
	for _, a := range acts {
		switch a.Kind {
		case action.AddReg:
			root, err := h.root(a.Hive)
			if err != nil {
				return err
			}
			if err := applyAddReg(root, a); err != nil {
				return err
			}
		case action.DelReg:
			root, err := h.root(a.Hive)
			if err != nil {
				return err
			}
			applyDelReg(root, a)
		default:
			// Not a registry action; another stage handles it.
		}
	}
	return nil
}

// root returns the root key of the hive an action targets.
func (h *Hives) root(hv action.RegHive) (*Key, error) {
	switch hv {
	case action.HiveSystem:
		return h.System.Root, nil
	case action.HiveSoftware:
		return h.Software.Root, nil
	case action.HiveDefault:
		return h.Default.Root, nil
	default:
		return nil, fmt.Errorf("hive: registry action targets unknown hive %q", hv)
	}
}

// applyAddReg creates the action's subkey (always) and, when the action carries
// a value, writes it. A key-only line (no value-name field) just ensures the key —
// matching setupapi, where a bare "type, subkey" AddReg line is a key-creation
// directive. HasValue is what distinguishes that from a value whose data is empty.
func applyAddReg(root *Key, a action.Action) error {
	key := root.EnsurePath(a.Subkey)
	if !a.HasValue {
		return nil // key-only
	}
	data, err := encodeActionData(a)
	if err != nil {
		return fmt.Errorf("hive: AddReg %q\\%q: %w", a.Subkey, a.ValueName, err)
	}
	key.SetValue(Value{Name: a.ValueName, Type: uint32(a.RegType), Data: data})
	return nil
}

// applyDelReg removes a value (when a value name is given) or the whole subkey.
// A path component that does not exist is a no-op.
func applyDelReg(root *Key, a action.Action) {
	if a.ValueName != "" {
		if key, ok := root.FindPath(a.Subkey); ok {
			key.DeleteValue(a.ValueName)
		}
		return
	}
	// Delete the subkey: find its parent, then drop the leaf.
	parentPath, leaf := splitSubkey(a.Subkey)
	if leaf == "" {
		return
	}
	if parent, ok := root.FindPath(parentPath); ok {
		parent.DeleteChild(leaf)
	}
}

// encodeActionData turns an action's textual/byte value into the on-disk bytes
// for its REG_* type. REG_SZ/REG_EXPAND_SZ come from Data; REG_DWORD parses Data
// as an integer; REG_MULTI_SZ splits Data on NUL (the separator plugin evaluation
// uses); REG_BINARY (and any other type) uses the raw DataBytes.
func encodeActionData(a action.Action) ([]byte, error) {
	switch a.RegType {
	case action.RegSZ, action.RegExpandSZ:
		return EncodeSZ(a.Data), nil
	case action.RegDWord:
		n, err := parseUint32(a.Data)
		if err != nil {
			return nil, fmt.Errorf("REG_DWORD value %q: %w", a.Data, err)
		}
		return EncodeDWord(n), nil
	case action.RegMultiSZ:
		var items []string
		if a.Data != "" {
			items = strings.Split(a.Data, "\x00")
		}
		return EncodeMultiSZ(items), nil
	default:
		// REG_BINARY, REG_NONE, and any high-word type carry raw bytes.
		return a.DataBytes, nil
	}
}

// splitSubkey splits a backslash path into its parent path and last component.
func splitSubkey(path string) (parent, leaf string) {
	path = strings.Trim(path, `\`)
	if i := strings.LastIndex(path, `\`); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

// parseUint32 reads a setupapi integer (0x-hex or decimal) as a 32-bit value,
// reusing the INF integer parser so the convention lives in one place.
func parseUint32(s string) (uint32, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	n, ok := inf.ParseInt(s)
	if !ok {
		return 0, fmt.Errorf("not an integer")
	}
	return uint32(n), nil
}
