// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"context"
	"encoding/binary"
	"fmt"

	wim "github.com/nradchenko/go-wim"
	"github.com/nradchenko/pe-builder/source"
)

// wimImageLabel is the image's name inside the WIM, as its XML records it.
const wimImageLabel = "XP"

// NT security-descriptor constants: a minimal self-relative descriptor is 20 bytes — a
// revision, a control word, and four (here all-zero) offsets.
const (
	seSelfRelative = 0x8000
	sdRevision     = 0x01
	sdHeaderLen    = 20 // rev+sbz+control(4) + 4 offsets(16)
)

// systemSecurity is the SecurityFunc a PE capture hands to go-wim. go-wim requires a non-empty
// security table, so every file points at one minimal, well-formed self-relative descriptor.
// Its contents do not matter to this boot: the wimmf overlay filter reads only the table's total
// length (to locate the root dentry), never a descriptor or a file's security_id, and it
// completes fabricated opens itself without one — so no access check runs against a WIM-supplied
// ACL. Boot-verified to the Luna desktop with the minimal descriptor.
func systemSecurity() wim.SecurityFunc {
	return wim.UniformSecurity(minimalSecurityDescriptor())
}

// minimalSecurityDescriptor is a well-formed self-relative NT descriptor with no owner, group,
// or ACLs — the least go-wim will accept for its (required non-empty) security table.
func minimalSecurityDescriptor() []byte {
	sd := make([]byte, sdHeaderLen)
	sd[0] = sdRevision
	binary.LittleEndian.PutUint16(sd[2:], seSelfRelative)
	return sd
}

// windowsVersion returns the Windows version to record in the image's XML for a source's build
// number. The major and minor are what that build is: 2600 is 5.1, 3790 is 5.2. The service-pack
// build is left unset rather than guessed — it is not derivable from the build number, and
// recording a plausible-looking wrong one is worse than recording none.
func windowsVersion(build int) wim.Version {
	v := wim.Version{Major: 5, Minor: 2, Build: build}
	if build <= 2600 {
		v.Minor = 1
	}
	return v
}

// captureWIM writes the assembled payload tree to wimPath as the bootable WIM the WIM media tail
// serves. The image's architecture and version come from the source rather than being assumed:
// the overlay filter reads the architecture and logs it at load, so an amd64 image claiming i386
// would misreport itself exactly where an x64 boot is debugged.
func captureWIM(ctx context.Context, treeDir, wimPath string, id source.Identity) error {
	arch := wim.ArchIntel
	if source.ResolveArch(id.Arch) == source.ArchAMD64 {
		arch = wim.ArchAMD64
	}
	image := wim.ImageInfo{
		Name:    wimImageLabel,
		Boot:    true,
		Windows: &wim.WindowsInfo{Arch: arch, Version: windowsVersion(id.Build)},
	}
	if err := wim.CaptureDir(ctx, treeDir, wimPath, image, wim.Options{
		Security: systemSecurity(),
	}); err != nil {
		return fmt.Errorf("capture WIM: %w", err)
	}
	return nil
}
