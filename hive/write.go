// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"encoding/binary"
	"fmt"
)

// Fixed record sizes (bytes before any variable-length name/data/list tail).
const (
	nkFixedLen  = 0x4C // CM_KEY_NODE up to the name
	vkFixedLen  = 0x14 // CM_KEY_VALUE up to the name
	skFixedLen  = 0x14 // CM_KEY_SECURITY up to the descriptor
	lhHeaderLen = 4    // leaf-index signature + count
	lhElemLen   = 8    // per subkey: node offset + name hash
)

// nk flag bits.
const (
	nkKeyHiveEntry = 0x0004 // the root key of the hive
	nkKeyNoDelete  = 0x0008 // the root key may not be deleted
)

// WriteHive serializes a tree into a fresh, canonically-laid-out regf image:
// a base block followed by hive bins of cells. Output is deterministic (fixed
// timestamps, stable ordering) so it can back golden fixtures. It returns an
// error rather than emit a malformed hive when a value needs big-data (db) or a
// key needs a root subkey index (ri) — cases deferred until a real hive needs
// them.
func WriteHive(h *Hive) ([]byte, error) {
	if h == nil || h.Root == nil {
		return nil, fmt.Errorf("hive: nil hive")
	}
	w := &regfWriter{hbinStart: -1, sd: buildSecurityDescriptor()}

	// The security descriptor is shared by every key; allocate it first.
	w.skOff = w.alloc(skFixedLen + len(w.sd))

	root, err := w.planKey(h.Root, none)
	if err != nil {
		return nil, err
	}
	w.finalizeHbin()

	w.writeSK()
	w.writeKey(root, true)

	base := buildBaseBlock(root.nkOff, uint32(len(w.buf)))
	return append(base, w.buf...), nil
}

// regfWriter lays out hive bins: it appends cells (allocation pass), then fills
// their bytes once all cross-reference offsets are known (write pass).
type regfWriter struct {
	buf       []byte // hive-bins data (file offset 0x1000 onward)
	hbinStart int    // buffer index of the open hbin header, or -1
	curHbinSz int    // size of the open hbin

	sd       []byte // shared security descriptor
	skOff    uint32 // offset of the shared sk cell
	keyCount int    // number of keys (the sk reference count)
}

// keyLayout records the offsets and encoded names assigned to one key's cells
// during the allocation pass, consumed (without re-encoding) during the write
// pass.
type keyLayout struct {
	key          *Key
	nkOff        uint32
	nameBytes    []byte // encoded key name
	nameComp     bool   // name uses the compressed form
	parentOff    uint32
	valueListOff uint32 // none if the key has no values
	values       []valLayout
	subkeyOff    uint32 // leaf-index cell offset, or none
	children     []*keyLayout
}

// valLayout records one value's encoded name and cell offsets.
type valLayout struct {
	value     Value
	nameBytes []byte
	nameComp  bool
	vkOff     uint32
	dataOff   uint32 // none when the value's data is stored inline (≤4 bytes)
}

// alloc reserves a cell with a payload of payloadSize bytes and returns its
// offset relative to the start of hive-bins data (the value cross-references
// use). The cell's size field is written now (negative == allocated); the
// payload is left zeroed for the write pass.
func (w *regfWriter) alloc(payloadSize int) uint32 {
	cellSize := align8(4 + payloadSize)
	w.ensureRoom(cellSize)
	off := uint32(len(w.buf))
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(int32(-cellSize)))
	w.buf = append(w.buf, hdr[:]...)
	w.buf = append(w.buf, make([]byte, cellSize-4)...)
	return off
}

// ensureRoom makes sure the open hbin can hold a cellSize-byte cell, closing it
// and opening a new (possibly larger) bin when it cannot.
func (w *regfWriter) ensureRoom(cellSize int) {
	if w.hbinStart >= 0 {
		if len(w.buf)-w.hbinStart+cellSize <= w.curHbinSz {
			return
		}
		w.finalizeHbin()
	}
	w.startHbin(cellSize)
}

// startHbin opens a new hbin at the current (4096-aligned) buffer end, sized to
// hold at least a firstCell-byte cell after its header.
func (w *regfWriter) startHbin(firstCell int) {
	w.hbinStart = len(w.buf)
	w.curHbinSz = roundUp(hbinHeaderLen+firstCell, hbinSize)
	hdr := make([]byte, hbinHeaderLen)
	copy(hdr[0:], sigHbin)
	binary.LittleEndian.PutUint32(hdr[0x04:], uint32(w.hbinStart))
	binary.LittleEndian.PutUint32(hdr[0x08:], uint32(w.curHbinSz))
	if w.hbinStart == 0 {
		binary.LittleEndian.PutUint64(hdr[0x14:], fixedFiletime)
	}
	w.buf = append(w.buf, hdr...)
}

// finalizeHbin pads the open hbin out to its declared size with a single free
// cell, so the next hbin starts on a 4096-byte boundary.
func (w *regfWriter) finalizeHbin() {
	if w.hbinStart < 0 {
		return
	}
	gap := w.hbinStart + w.curHbinSz - len(w.buf)
	if gap > 0 {
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(gap)) // positive == free
		w.buf = append(w.buf, hdr[:]...)
		w.buf = append(w.buf, make([]byte, gap-4)...)
	}
	w.hbinStart = -1
}

// planKey allocates the cells for k and its subtree, returning their layout.
func (w *regfWriter) planKey(k *Key, parentOff uint32) (*keyLayout, error) {
	w.keyCount++
	nameBytes, nameComp := encodeName(k.Name)
	L := &keyLayout{
		key:          k,
		nkOff:        w.alloc(nkFixedLen + len(nameBytes)),
		nameBytes:    nameBytes,
		nameComp:     nameComp,
		parentOff:    parentOff,
		valueListOff: none,
		subkeyOff:    none,
	}

	if len(k.Values) > 0 {
		L.valueListOff = w.alloc(4 * len(k.Values))
		for _, v := range k.Values {
			if len(v.Data) > maxCellData {
				return nil, fmt.Errorf("hive: big-data (db) not yet implemented for value %q (%d bytes)", v.Name, len(v.Data))
			}
			nameB, comp := encodeName(v.Name)
			// Data of four bytes or fewer is stored inline in the vk record (as
			// Windows does); only larger data needs its own cell.
			dataOff := none
			vkOff := w.alloc(vkFixedLen + len(nameB))
			if len(v.Data) > 4 {
				dataOff = w.alloc(len(v.Data))
			}
			L.values = append(L.values, valLayout{
				value: v, nameBytes: nameB, nameComp: comp, vkOff: vkOff, dataOff: dataOff,
			})
		}
	}

	if len(k.Subkeys) > 0 {
		if len(k.Subkeys) > maxSubkeys {
			return nil, fmt.Errorf("hive: subkey-list root (ri) not yet implemented for key %q (%d subkeys)", k.Name, len(k.Subkeys))
		}
		children := sortedChildren(k.Subkeys)
		L.subkeyOff = w.alloc(lhHeaderLen + lhElemLen*len(children))
		for _, c := range children {
			cl, err := w.planKey(c, L.nkOff)
			if err != nil {
				return nil, err
			}
			L.children = append(L.children, cl)
		}
	}
	return L, nil
}

// at returns the cell payload slice for offset off (skipping the 4-byte size).
func (w *regfWriter) at(off uint32) []byte { return w.buf[off+4:] }

// writeSK fills the shared security-descriptor cell.
func (w *regfWriter) writeSK() {
	p := w.at(w.skOff)
	copy(p[0:], sigSK)
	binary.LittleEndian.PutUint16(p[0x02:], 0)
	binary.LittleEndian.PutUint32(p[0x04:], w.skOff) // flink (self: single sk)
	binary.LittleEndian.PutUint32(p[0x08:], w.skOff) // blink (self)
	binary.LittleEndian.PutUint32(p[0x0C:], uint32(w.keyCount))
	binary.LittleEndian.PutUint32(p[0x10:], uint32(len(w.sd)))
	copy(p[0x14:], w.sd)
}

// writeKey fills a key's nk, its value list/values/data, and its subkey index,
// then recurses into its children.
func (w *regfWriter) writeKey(L *keyLayout, isRoot bool) {
	nk := w.at(L.nkOff)
	copy(nk[0:], sigNK)
	var flags uint16
	if L.nameComp {
		flags |= nkCompName
	}
	if isRoot {
		flags |= nkKeyHiveEntry | nkKeyNoDelete
	}
	binary.LittleEndian.PutUint16(nk[0x02:], flags)
	binary.LittleEndian.PutUint64(nk[0x04:], fixedFiletime)
	binary.LittleEndian.PutUint32(nk[0x10:], L.parentOff)
	binary.LittleEndian.PutUint32(nk[0x14:], uint32(len(L.children))) // stable subkey count
	binary.LittleEndian.PutUint32(nk[0x18:], 0)                       // volatile subkey count
	binary.LittleEndian.PutUint32(nk[0x1C:], L.subkeyOff)             // stable subkey list
	binary.LittleEndian.PutUint32(nk[0x20:], none)                    // volatile subkey list
	binary.LittleEndian.PutUint32(nk[0x24:], uint32(len(L.values)))
	binary.LittleEndian.PutUint32(nk[0x28:], L.valueListOff)
	binary.LittleEndian.PutUint32(nk[0x2C:], w.skOff)
	binary.LittleEndian.PutUint32(nk[0x30:], none) // class name
	binary.LittleEndian.PutUint16(nk[0x48:], uint16(len(L.nameBytes)))
	binary.LittleEndian.PutUint16(nk[0x4A:], 0) // class length
	copy(nk[nkFixedLen:], L.nameBytes)

	if L.valueListOff != none {
		list := w.at(L.valueListOff)
		for i := range L.values {
			binary.LittleEndian.PutUint32(list[4*i:], L.values[i].vkOff)
			w.writeValue(&L.values[i])
		}
	}

	if L.subkeyOff != none {
		lh := w.at(L.subkeyOff)
		copy(lh[0:], sigLH)
		binary.LittleEndian.PutUint16(lh[0x02:], uint16(len(L.children)))
		for i, c := range L.children {
			el := lh[lhHeaderLen+lhElemLen*i:]
			binary.LittleEndian.PutUint32(el[0:], c.nkOff)
			binary.LittleEndian.PutUint32(el[4:], nameHash(c.key.Name))
		}
		for _, c := range L.children {
			w.writeKey(c, false)
		}
	}
}

// writeValue fills a vk cell and, for data larger than four bytes, its data cell.
func (w *regfWriter) writeValue(vl *valLayout) {
	vk := w.at(vl.vkOff)
	copy(vk[0:], sigVK)
	binary.LittleEndian.PutUint16(vk[0x02:], uint16(len(vl.nameBytes)))
	if vl.dataOff == none {
		// Four bytes or fewer: store the data inline in the Data field with the
		// top bit of the length set. This matches Windows, avoids a dangling data
		// offset for empty values, and keeps small values (e.g. a REG_DWORD whose
		// bytes happen to begin "db") out of a separate cell that a reader could
		// misread as a big-data record.
		binary.LittleEndian.PutUint32(vk[0x04:], 0x80000000|uint32(len(vl.value.Data)))
		var inline [4]byte
		copy(inline[:], vl.value.Data)
		copy(vk[0x08:], inline[:])
	} else {
		binary.LittleEndian.PutUint32(vk[0x04:], uint32(len(vl.value.Data)))
		binary.LittleEndian.PutUint32(vk[0x08:], vl.dataOff)
	}
	binary.LittleEndian.PutUint32(vk[0x0C:], vl.value.Type)
	var flags uint16
	if vl.nameComp && len(vl.nameBytes) > 0 {
		flags |= vkCompName
	}
	binary.LittleEndian.PutUint16(vk[0x10:], flags)
	copy(vk[vkFixedLen:], vl.nameBytes)

	if vl.dataOff != none {
		copy(w.at(vl.dataOff), vl.value.Data)
	}
}
