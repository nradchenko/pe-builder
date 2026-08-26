// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"www.velocidex.com/golang/regparser"
)

// The property these tests check is that every key of a hive we write carries a DACL whose
// grants are inheritable by subkeys. It is not about the keys themselves — those get the
// descriptor either way — but about keys the *running system* creates underneath them. A
// subkey inherits the parent's inheritable ACEs, or, if the parent has none, falls back to
// the creating token's default DACL, which names SYSTEM and Administrators and nobody else.
// A svchost-hosted service running as NetworkService or LocalService then cannot read its
// own configuration and its host exits before reporting ready.
//
// What no test here can show is that Windows honours the flag. That is kernel behaviour,
// established by booting an image; these tests establish only that we emit it — which is
// the half a refactor can silently undo.

// Security-descriptor and ACL constants, from the on-disk formats these tests read.
const (
	seSelfRelative = 0x8000 // SECURITY_DESCRIPTOR_CONTROL: SE_SELF_RELATIVE
	seDaclPresent  = 0x0004 // SECURITY_DESCRIPTOR_CONTROL: SE_DACL_PRESENT
	aclRevision    = 2      // ACL_REVISION
	accessAllowed  = 0      // ACCESS_ALLOWED_ACE_TYPE
	keyAllAccess   = 0x000F003F
)

// parsedACE is one ACE of a DACL, in the terms these tests assert on.
type parsedACE struct {
	Type  byte
	Flags byte
	Mask  uint32
	SID   string
}

// skDescriptor returns the security descriptor held in the sk cell at off, which is the
// hive-bin-relative offset an nk record stores. Hive-bin data begins at file offset 0x1000
// and every cell opens with its own four-byte size, which the offset does not include.
func skDescriptor(t *testing.T, data []byte, off uint32) []byte {
	t.Helper()
	const hbinBase = 0x1000
	start := hbinBase + int(off) + 4
	if start < 0 || start+skFixedLen > len(data) {
		t.Fatalf("sk offset %#x is outside a %d-byte hive", off, len(data))
	}
	p := data[start:]
	if got := p[:2]; !bytes.Equal(got, sigSK) {
		t.Fatalf("sk offset %#x: signature = %q, want %q", off, got, sigSK)
	}
	n := int(binary.LittleEndian.Uint32(p[0x10:]))
	if n < 0 || skFixedLen+n > len(p) {
		t.Fatalf("sk offset %#x: descriptor length %d overruns the hive", off, n)
	}
	return p[skFixedLen : skFixedLen+n]
}

// parseDACL walks a self-relative security descriptor's DACL. It checks the structural
// invariants as it goes, so a malformed descriptor fails here with a description of what
// is wrong rather than further along as a confusing mismatch.
func parseDACL(t *testing.T, sd []byte, label string) []parsedACE {
	t.Helper()
	if len(sd) < 20 {
		t.Fatalf("%s: security descriptor is %d bytes, shorter than its own header", label, len(sd))
	}
	if sd[0] != 1 {
		t.Errorf("%s: descriptor revision = %d, want 1", label, sd[0])
	}
	if ctl := binary.LittleEndian.Uint16(sd[2:]); ctl&seSelfRelative == 0 || ctl&seDaclPresent == 0 {
		t.Fatalf("%s: control = %#04x, want SE_SELF_RELATIVE|SE_DACL_PRESENT set", label, ctl)
	}
	daclOff := int(binary.LittleEndian.Uint32(sd[16:]))
	if daclOff <= 0 || daclOff+8 > len(sd) {
		t.Fatalf("%s: DACL offset %d is outside a %d-byte descriptor", label, daclOff, len(sd))
	}
	acl := sd[daclOff:]
	if acl[0] != aclRevision {
		t.Errorf("%s: ACL revision = %d, want %d", label, acl[0], aclRevision)
	}
	aclSize := int(binary.LittleEndian.Uint16(acl[2:]))
	count := int(binary.LittleEndian.Uint16(acl[4:]))
	if aclSize < 8 || aclSize > len(acl) {
		t.Fatalf("%s: AclSize = %d, outside the %d bytes that follow it", label, aclSize, len(acl))
	}

	aces := make([]parsedACE, 0, count)
	p := acl[8:aclSize]
	for i := 0; i < count; i++ {
		if len(p) < 8 {
			t.Fatalf("%s: ACE %d: %d bytes left, shorter than an ACE header", label, i, len(p))
		}
		size := int(binary.LittleEndian.Uint16(p[2:]))
		if size < 8 || size > len(p) {
			t.Fatalf("%s: ACE %d: AceSize = %d, outside the %d bytes that follow it", label, i, size, len(p))
		}
		aces = append(aces, parsedACE{
			Type:  p[0],
			Flags: p[1],
			Mask:  binary.LittleEndian.Uint32(p[4:]),
			SID:   formatSID(t, p[8:size], fmt.Sprintf("%s: ACE %d", label, i)),
		})
		p = p[size:]
	}
	if len(p) != 0 {
		t.Errorf("%s: %d bytes left over after %d ACEs", label, len(p), count)
	}
	return aces
}

// formatSID renders a SID in the S-R-I-S… form, so a failure names the account rather
// than printing bytes.
func formatSID(t *testing.T, b []byte, label string) string {
	t.Helper()
	if len(b) < 8 {
		t.Fatalf("%s: SID is %d bytes, shorter than its own header", label, len(b))
	}
	n := int(b[1])
	if len(b) < 8+4*n {
		t.Fatalf("%s: SID declares %d sub-authorities but holds %d bytes", label, n, len(b))
	}
	var authority uint64 // IdentifierAuthority is a 48-bit big-endian field
	for _, x := range b[2:8] {
		authority = authority<<8 | uint64(x)
	}
	s := fmt.Sprintf("S-%d-%d", b[0], authority)
	for i := 0; i < n; i++ {
		s += fmt.Sprintf("-%d", binary.LittleEndian.Uint32(b[8+4*i:]))
	}
	return s
}

// checkInheritable walks every key of a written hive and fails for each ACE that is not
// marked inheritable by subkeys.
func checkInheritable(t *testing.T, data []byte, label string) {
	t.Helper()
	reg, err := regparser.NewRegistry(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: NewRegistry: %v", label, err)
	}
	checked := 0
	var walk func(k *regparser.CM_KEY_NODE, path string)
	walk = func(k *regparser.CM_KEY_NODE, path string) {
		checked++
		sd := skDescriptor(t, data, k.Security())
		for i, a := range parseDACL(t, sd, label+": "+path) {
			if a.Flags&containerInheritACE == 0 {
				t.Errorf("%s: %s: ACE %d (%s) has flags %#02x, CONTAINER_INHERIT_ACE not set — "+
					"a key created under this one at run time would not inherit the grant",
					label, path, i, a.SID, a.Flags)
			}
		}
		for _, c := range k.Subkeys() {
			walk(c, path+`\`+c.Name())
		}
	}
	root := reg.OpenKey(`\`)
	if root == nil {
		t.Fatalf("%s: OpenKey(root) returned nil", label)
	}
	walk(root, root.Name())
	if checked == 0 {
		t.Fatalf("%s: walked no keys", label)
	}
	t.Logf("%s: checked %d keys", label, checked)
}

// TestWrittenKeysGrantInheritably is the regression test for the fix itself: every key of
// a hive we write must offer its grants to the subkeys the running system creates under it.
func TestWrittenKeysGrantInheritably(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	checkInheritable(t, data, "written")
}

// TestSecurityDescriptorGrants pins what the shared descriptor actually says. The walk above
// would pass just as well on a descriptor granting the wrong accounts the wrong access, so
// the grant is asserted separately from its inheritance.
func TestSecurityDescriptorGrants(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	reg, err := regparser.NewRegistry(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	root := reg.OpenKey(`\`)
	if root == nil {
		t.Fatalf("OpenKey(root) returned nil")
	}
	got := parseDACL(t, skDescriptor(t, data, root.Security()), "root")

	want := []parsedACE{
		{Type: accessAllowed, Flags: containerInheritACE, Mask: keyAllAccess, SID: "S-1-5-18"},     // LocalSystem
		{Type: accessAllowed, Flags: containerInheritACE, Mask: keyAllAccess, SID: "S-1-5-32-544"}, // BUILTIN\Administrators
		{Type: accessAllowed, Flags: containerInheritACE, Mask: keyAllAccess, SID: "S-1-1-0"},      // Everyone
	}
	if len(got) != len(want) {
		t.Fatalf("DACL has %d ACEs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ACE %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestOneDescriptorPerHive checks the design every key relies on: a single sk cell shared by
// the whole hive. It is what makes the walk above cheap, and a writer that started emitting
// one descriptor per key would be a silent change of shape rather than a test failure.
func TestOneDescriptorPerHive(t *testing.T) {
	data, err := WriteHive(sampleHive())
	if err != nil {
		t.Fatalf("WriteHive: %v", err)
	}
	reg, err := regparser.NewRegistry(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	seen := map[uint32]string{}
	var walk func(k *regparser.CM_KEY_NODE, path string)
	walk = func(k *regparser.CM_KEY_NODE, path string) {
		if _, ok := seen[k.Security()]; !ok {
			seen[k.Security()] = path
		}
		for _, c := range k.Subkeys() {
			walk(c, path+`\`+c.Name())
		}
	}
	root := reg.OpenKey(`\`)
	if root == nil {
		t.Fatalf("OpenKey(root) returned nil")
	}
	walk(root, root.Name())
	if len(seen) != 1 {
		t.Errorf("hive references %d security descriptors, want 1: %v", len(seen), seen)
	}
}
