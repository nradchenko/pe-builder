// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"encoding/binary"
)

// This file builds the self-authored PE and cabinet fixtures the tests resolve
// and walk. Nothing here is Microsoft-derived: buildPE emits a minimal, valid
// PE32 image whose only meaningful content is a static and delay-load import
// table naming the given modules, and writeCabinet emits a store-only (compNone)
// MSCF cabinet — enough to exercise every resolution path (loose, compressed
// loose, cabinet member) without any real binary.

const (
	fixtureImageBase   = 0x00400000
	fixtureSectionRVA  = 0x1000 // .idata virtual address
	fixtureRawStart    = 0x200  // .idata file offset (== SizeOfHeaders)
	fixturePEOffset    = 0x40   // e_lfanew
	fixtureOptHdrSize  = 0xE0   // PE32 optional header with 16 data directories
	dirEntryImport     = 1
	dirEntryDelayImp   = 13
	numRvaAndSizes     = 16
	fileDLLCharacteris = 0x2000 // IMAGE_FILE_DLL
)

// buildPE returns a minimal PE32 image that statically imports the modules in
// imports and delay-loads those in delay. isDLL sets the DLL characteristic bit.
func buildPE(imports, delay []string, isDLL bool) []byte {
	// --- lay out the .idata section --------------------------------------
	// The section maps RVA→file-offset as identity within itself only via the
	// section table; here we track RVAs directly. The import and delay-import
	// directory tables come first, then a heap of thunk tables and strings they
	// reference.
	idtSize := (len(imports) + 1) * 20
	didtSize := 0
	if len(delay) > 0 {
		didtSize = (len(delay) + 1) * 32
	}
	heapRVA := uint32(fixtureSectionRVA + idtSize + didtSize)

	var heap []byte
	// appendThunkAndName appends an IMAGE_IMPORT_BY_NAME (hint+name), a two-entry
	// thunk table pointing at it, and the DLL name string; it returns the RVAs of
	// the thunk table and the DLL name.
	appendThunkAndName := func(dll string) (thunkRVA, nameRVA uint32) {
		hintNameRVA := heapRVA + uint32(len(heap))
		heap = append(heap, 0, 0) // hint
		heap = append(heap, dll...)
		heap = append(heap, 0)
		if len(heap)%2 == 1 {
			heap = append(heap, 0)
		}
		thunkRVA = heapRVA + uint32(len(heap))
		heap = binary.LittleEndian.AppendUint32(heap, hintNameRVA) // import by name
		heap = binary.LittleEndian.AppendUint32(heap, 0)           // null terminator
		nameRVA = heapRVA + uint32(len(heap))
		heap = append(heap, dll...)
		heap = append(heap, 0)
		if len(heap)%2 == 1 {
			heap = append(heap, 0)
		}
		return thunkRVA, nameRVA
	}

	// Import Directory Table.
	var idt []byte
	for _, dll := range imports {
		thunkRVA, nameRVA := appendThunkAndName(dll)
		idt = binary.LittleEndian.AppendUint32(idt, thunkRVA) // OriginalFirstThunk
		idt = binary.LittleEndian.AppendUint32(idt, 0)        // TimeDateStamp
		idt = binary.LittleEndian.AppendUint32(idt, 0)        // ForwarderChain
		idt = binary.LittleEndian.AppendUint32(idt, nameRVA)  // Name
		idt = binary.LittleEndian.AppendUint32(idt, thunkRVA) // FirstThunk
	}
	idt = append(idt, make([]byte, 20)...) // null descriptor

	// Delay Import Directory Table (new-style: RVAs, Attributes=1).
	var didt []byte
	for _, dll := range delay {
		thunkRVA, nameRVA := appendThunkAndName(dll)
		didt = binary.LittleEndian.AppendUint32(didt, 1)        // Attributes
		didt = binary.LittleEndian.AppendUint32(didt, nameRVA)  // Name
		didt = binary.LittleEndian.AppendUint32(didt, 0)        // ModuleHandleRVA
		didt = binary.LittleEndian.AppendUint32(didt, thunkRVA) // ImportAddressTableRVA
		didt = binary.LittleEndian.AppendUint32(didt, thunkRVA) // ImportNameTableRVA
		didt = binary.LittleEndian.AppendUint32(didt, 0)        // BoundImportAddressTableRVA
		didt = binary.LittleEndian.AppendUint32(didt, 0)        // UnloadInformationTableRVA
		didt = binary.LittleEndian.AppendUint32(didt, 0)        // TimeDateStamp
	}
	if len(delay) > 0 {
		didt = append(didt, make([]byte, 32)...) // null descriptor
	}

	idata := append(append(idt, didt...), heap...)

	// --- assemble the file ----------------------------------------------
	rawSize := align(uint32(len(idata)), fixtureRawStart)
	buf := make([]byte, fixtureRawStart+rawSize)

	// DOS header.
	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3C:], fixturePEOffset)

	// PE signature + COFF header.
	p := fixturePEOffset
	copy(buf[p:], "PE\x00\x00")
	coff := p + 4
	binary.LittleEndian.PutUint16(buf[coff:], 0x014C) // Machine i386
	binary.LittleEndian.PutUint16(buf[coff+2:], 1)    // NumberOfSections
	binary.LittleEndian.PutUint16(buf[coff+16:], fixtureOptHdrSize)
	chars := uint16(0x0102) // EXECUTABLE_IMAGE | 32BIT_MACHINE
	if isDLL {
		chars |= fileDLLCharacteris
	}
	binary.LittleEndian.PutUint16(buf[coff+18:], chars)

	// Optional header (PE32).
	opt := coff + 20
	binary.LittleEndian.PutUint16(buf[opt:], 0x010B) // Magic PE32
	sizeOfImage := align(fixtureSectionRVA+uint32(len(idata)), 0x1000)
	binary.LittleEndian.PutUint32(buf[opt+16:], fixtureSectionRVA) // AddressOfEntryPoint (nominal)
	binary.LittleEndian.PutUint32(buf[opt+20:], fixtureSectionRVA) // BaseOfCode
	binary.LittleEndian.PutUint32(buf[opt+28:], fixtureImageBase)  // ImageBase
	binary.LittleEndian.PutUint32(buf[opt+32:], 0x1000)            // SectionAlignment
	binary.LittleEndian.PutUint32(buf[opt+36:], fixtureRawStart)   // FileAlignment
	binary.LittleEndian.PutUint16(buf[opt+40:], 4)                 // MajorOperatingSystemVersion
	binary.LittleEndian.PutUint16(buf[opt+48:], 4)                 // MajorSubsystemVersion
	binary.LittleEndian.PutUint32(buf[opt+56:], sizeOfImage)       // SizeOfImage
	binary.LittleEndian.PutUint32(buf[opt+60:], fixtureRawStart)   // SizeOfHeaders
	binary.LittleEndian.PutUint16(buf[opt+68:], 3)                 // Subsystem = console
	binary.LittleEndian.PutUint32(buf[opt+92:], numRvaAndSizes)    // NumberOfRvaAndSizes

	// Data directories begin right after the 96-byte PE32 optional-header fields.
	dirs := opt + 96
	binary.LittleEndian.PutUint32(buf[dirs+dirEntryImport*8:], fixtureSectionRVA)
	binary.LittleEndian.PutUint32(buf[dirs+dirEntryImport*8+4:], uint32(idtSize))
	if didtSize > 0 {
		delayRVA := uint32(fixtureSectionRVA + idtSize)
		binary.LittleEndian.PutUint32(buf[dirs+dirEntryDelayImp*8:], delayRVA)
		binary.LittleEndian.PutUint32(buf[dirs+dirEntryDelayImp*8+4:], uint32(didtSize))
	}

	// Section header (.idata).
	sec := opt + fixtureOptHdrSize
	copy(buf[sec:], ".idata\x00\x00")
	binary.LittleEndian.PutUint32(buf[sec+8:], uint32(len(idata))) // VirtualSize
	binary.LittleEndian.PutUint32(buf[sec+12:], fixtureSectionRVA) // VirtualAddress
	binary.LittleEndian.PutUint32(buf[sec+16:], rawSize)           // SizeOfRawData
	binary.LittleEndian.PutUint32(buf[sec+20:], fixtureRawStart)   // PointerToRawData
	binary.LittleEndian.PutUint32(buf[sec+36:], 0xC0000040)        // INITIALIZED_DATA|READ|WRITE

	copy(buf[fixtureRawStart:], idata)
	return buf
}

// align rounds n up to the next multiple of a (a a power of two).
func align(n, a uint32) uint32 {
	return (n + a - 1) &^ (a - 1)
}

// cabMember is one file to place in a fixture cabinet.
type cabMember struct {
	name string
	data []byte
}

// writeCabinet returns a single-folder, store-only (uncompressed) MSCF cabinet
// holding members — the format the compress package reads for both loose *.??_
// files (one member) and multi-file cabinets (several).
func writeCabinet(members []cabMember) []byte {
	const (
		headerSize = 36
		folderSize = 8
		fileEntry  = 16 // fixed CFFILE fields before the name
	)
	coffFiles := headerSize + folderSize
	filesLen := 0
	for _, m := range members {
		filesLen += fileEntry + len(m.name) + 1
	}
	coffData := coffFiles + filesLen

	var folder []byte
	for _, m := range members {
		folder = append(folder, m.data...)
	}

	out := make([]byte, 0, coffData+4+4+len(folder))
	// CFHEADER.
	out = append(out, "MSCF"...)
	out = binary.LittleEndian.AppendUint32(out, 0)                 // reserved1
	out = binary.LittleEndian.AppendUint32(out, 0)                 // cbCabinet (ignored)
	out = binary.LittleEndian.AppendUint32(out, 0)                 // reserved2
	out = binary.LittleEndian.AppendUint32(out, uint32(coffFiles)) // coffFiles
	out = binary.LittleEndian.AppendUint32(out, 0)                 // reserved3
	out = append(out, 3, 1)                                        // versionMinor, versionMajor
	out = binary.LittleEndian.AppendUint16(out, 1)                 // cFolders
	out = binary.LittleEndian.AppendUint16(out, uint16(len(members)))
	out = binary.LittleEndian.AppendUint16(out, 0) // flags
	out = binary.LittleEndian.AppendUint16(out, 0) // setID
	out = binary.LittleEndian.AppendUint16(out, 0) // iCabinet
	// CFFOLDER.
	out = binary.LittleEndian.AppendUint32(out, uint32(coffData)) // coffCabStart
	out = binary.LittleEndian.AppendUint16(out, 1)                // cCFData (blocks)
	out = binary.LittleEndian.AppendUint16(out, 0)                // typeCompress = none
	// CFFILE entries.
	offset := uint32(0)
	for _, m := range members {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(m.data))) // cbFile
		out = binary.LittleEndian.AppendUint32(out, offset)              // uoffFolderStart
		out = binary.LittleEndian.AppendUint16(out, 0)                   // iFolder
		out = binary.LittleEndian.AppendUint16(out, 0)                   // date
		out = binary.LittleEndian.AppendUint16(out, 0)                   // time
		out = binary.LittleEndian.AppendUint16(out, 0)                   // attribs
		out = append(out, m.name...)
		out = append(out, 0)
		offset += uint32(len(m.data))
	}
	// CFDATA (single store-only block).
	out = binary.LittleEndian.AppendUint32(out, 0)                   // csum
	out = binary.LittleEndian.AppendUint16(out, uint16(len(folder))) // cbData
	out = binary.LittleEndian.AppendUint16(out, uint16(len(folder))) // cbUncomp
	out = append(out, folder...)
	return out
}
