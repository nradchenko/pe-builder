// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"encoding/binary"
	"testing"
)

// TestMinimalSecurityDescriptor checks the descriptor every dentry points at is a well-formed
// self-relative NT security descriptor: 20 bytes, revision 1, the self-relative control bit set,
// and no owner/group/SACL/DACL (all four offsets zero). go-wim requires the security table be
// non-empty; the overlay filter reads only its length, so this minimal descriptor is enough.
func TestMinimalSecurityDescriptor(t *testing.T) {
	sd := minimalSecurityDescriptor()
	if len(sd) != sdHeaderLen {
		t.Fatalf("SD length = %d, want %d", len(sd), sdHeaderLen)
	}
	if sd[0] != sdRevision {
		t.Errorf("SD revision = %d, want %d", sd[0], sdRevision)
	}
	if ctrl := binary.LittleEndian.Uint16(sd[2:]); ctrl != seSelfRelative {
		t.Errorf("SD control = %#x, want %#x (self-relative only)", ctrl, seSelfRelative)
	}
	for _, off := range []int{4, 8, 12, 16} { // owner, group, SACL, DACL offsets
		if v := binary.LittleEndian.Uint32(sd[off:]); v != 0 {
			t.Errorf("offset field at byte %d = %d, want 0", off, v)
		}
	}
}
