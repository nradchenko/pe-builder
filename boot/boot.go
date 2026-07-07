// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nradchenko/pe-builder/source"
)

// BootSectName is the boot-image file the ISO master hands to El Torito. It lives
// at the output-tree root (the ISO root), not under the system-root directory, and
// is hidden from the file tree by the master.
const BootSectName = "bootsect.bin"

// startnetName is the startup script winlogon's Setup\CmdLine runs; it lives in
// system32 alongside cmd.exe.
const startnetName = "startnet.cmd"

// bootSectPath is the path bootsect.bin is written to under an output tree.
func bootSectPath(outDir string) string {
	return filepath.Join(outDir, BootSectName)
}

// WriteStartnet places the built-in startup script at system32\startnet.cmd in the
// tree. The script is the environment's own file (not a source copy), so it is
// written here rather than by the file-copy stage; winlogon runs it via
// SYSTEM\Setup\CmdLine once the environment is up.
func WriteStartnet(treeRoot string, content []byte) error {
	dir := filepath.Join(treeRoot, source.SystemRoot, "system32")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("boot: create system32 dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, startnetName), content, 0o644); err != nil {
		return fmt.Errorf("boot: write %s: %w", startnetName, err)
	}
	return nil
}
