// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

// Package compress decodes the compressed file containers used by a Windows
// installation source. It sniffs a stream's container magic and dispatches to
// the matching decoder.
//
// Windows XP/2003 sources compress everything with cabinet-framed LZX: loose
// single-file cabinets (the *.??_ files, e.g. NTDLL.DL_) and the multi-file
// DRIVER.CAB/SP3.CAB. Those are handled here. The SZDD/KWAJ and MSZIP formats
// are not observed on those sources and are deferred until one that uses them
// appears.
package compress

import (
	"errors"
	"fmt"
	"io"
)

// Decompress reads a compressed source file and returns its plaintext. The
// stream must be a single-file MSCF cabinet, as used by the loose *.??_ files;
// for multi-file cabinets use ReadCabinet to select a member.
func Decompress(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return DecompressBytes(data)
}

// DecompressBytes is Decompress for data already held in memory.
func DecompressBytes(data []byte) ([]byte, error) {
	if !IsCabinet(data) {
		return nil, errors.New("unrecognized compression container (expected MSCF cabinet)")
	}
	cab, err := ReadCabinet(data)
	if err != nil {
		return nil, err
	}
	files := cab.Files()
	if len(files) != 1 {
		return nil, fmt.Errorf("cabinet holds %d files; use ReadCabinet to select one", len(files))
	}
	return cab.Data(files[0])
}
