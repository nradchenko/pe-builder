// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import "encoding/binary"

// buildSecurityDescriptor returns a self-relative security descriptor granting
// full control to LocalSystem, the Administrators group, and Everyone. A single
// shared instance of this descriptor is referenced by every key in a hive we
// write — a deliberate simplification that is safe for a RAM-resident PE running
// as SYSTEM (no per-key ACLs).
func buildSecurityDescriptor() []byte {
	// Well-known SIDs, in on-disk (little-endian) form.
	system := encodeSID(5, 18)      // S-1-5-18  LocalSystem
	admins := encodeSID(5, 32, 544) // S-1-5-32-544  BUILTIN\Administrators
	everyone := encodeSID(1, 0)     // S-1-1-0  Everyone
	const keyAllAccess = 0x000F003F

	dacl := buildACL(
		accessAllowedACE(keyAllAccess, system),
		accessAllowedACE(keyAllAccess, admins),
		accessAllowedACE(keyAllAccess, everyone),
	)

	// Self-relative SECURITY_DESCRIPTOR: fixed 20-byte header followed by the
	// owner SID, group SID, and DACL, each referenced by a relative offset.
	const headerLen = 20
	owner := admins
	group := admins
	ownerOff := headerLen
	groupOff := ownerOff + len(owner)
	daclOff := groupOff + len(group)

	sd := make([]byte, daclOff+len(dacl))
	sd[0] = 1 // Revision
	sd[1] = 0 // Sbz1
	// Control: SE_SELF_RELATIVE (0x8000) | SE_DACL_PRESENT (0x0004).
	binary.LittleEndian.PutUint16(sd[2:], 0x8004)
	binary.LittleEndian.PutUint32(sd[4:], uint32(ownerOff))
	binary.LittleEndian.PutUint32(sd[8:], uint32(groupOff))
	binary.LittleEndian.PutUint32(sd[12:], 0) // Sacl offset (none)
	binary.LittleEndian.PutUint32(sd[16:], uint32(daclOff))
	copy(sd[ownerOff:], owner)
	copy(sd[groupOff:], group)
	copy(sd[daclOff:], dacl)
	return sd
}

// encodeSID builds a SID from its identifier authority and sub-authorities, in
// on-disk form. Only the low byte of the 48-bit authority is used, which covers
// every well-known SID needed here (NT authority 5, World authority 1).
func encodeSID(authority byte, subAuths ...uint32) []byte {
	sid := make([]byte, 8+4*len(subAuths))
	sid[0] = 1                   // Revision
	sid[1] = byte(len(subAuths)) // SubAuthorityCount
	sid[7] = authority           // IdentifierAuthority (big-endian 48-bit; only low byte used)
	for i, sa := range subAuths {
		binary.LittleEndian.PutUint32(sid[8+4*i:], sa)
	}
	return sid
}

// accessAllowedACE builds an ACCESS_ALLOWED_ACE granting mask to sid.
func accessAllowedACE(mask uint32, sid []byte) []byte {
	size := 8 + len(sid)
	ace := make([]byte, size)
	ace[0] = 0 // AceType: ACCESS_ALLOWED_ACE_TYPE
	ace[1] = 0 // AceFlags
	binary.LittleEndian.PutUint16(ace[2:], uint16(size))
	binary.LittleEndian.PutUint32(ace[4:], mask)
	copy(ace[8:], sid)
	return ace
}

// buildACL assembles a DACL from the given ACEs.
func buildACL(aces ...[]byte) []byte {
	size := 8
	for _, a := range aces {
		size += len(a)
	}
	acl := make([]byte, size)
	acl[0] = 2 // AclRevision: ACL_REVISION
	acl[1] = 0 // Sbz1
	binary.LittleEndian.PutUint16(acl[2:], uint16(size))
	binary.LittleEndian.PutUint16(acl[4:], uint16(len(aces)))
	binary.LittleEndian.PutUint16(acl[6:], 0) // Sbz2
	off := 8
	for _, a := range aces {
		copy(acl[off:], a)
		off += len(a)
	}
	return acl
}
