// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/nradchenko/pe-builder/internal/peresource"
)

// fileVersion is a four-part Windows file version (e.g. 5.1.2600.5512).
type fileVersion struct {
	Major, Minor, Build, Revision int
}

func (v fileVersion) String() string {
	return fmt.Sprintf("%d.%d.%d.%d", v.Major, v.Minor, v.Build, v.Revision)
}

// atLeast reports whether v is greater than or equal to o, compared part by part.
func (v fileVersion) atLeast(o fileVersion) bool {
	for _, d := range [][2]int{
		{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Build, o.Build}, {v.Revision, o.Revision},
	} {
		if d[0] != d[1] {
			return d[0] > d[1]
		}
	}
	return true
}

// vsFixedFileInfoSig marks the VS_FIXEDFILEINFO struct within a version blob.
const vsFixedFileInfoSig = 0xFEEF04BD

// peFileVersion reads the FileVersion from a PE image's version resource: the first
// RT_VERSION resource the image carries, whose blob holds a VS_FIXEDFILEINFO.
//
// Deliberately the first and no further. This decides whether a source is supported at
// all, so a binary whose version block reads cleanly must not be rejected over some
// later resource that does not — the probe has the answer it came for by then.
func peFileVersion(image []byte) (fileVersion, error) {
	e, ok, err := peresource.ReadFirst(image, peresource.TypeVersion)
	if err != nil {
		return fileVersion{}, err
	}
	if !ok {
		return fileVersion{}, fmt.Errorf("no version resource")
	}
	return parseFixedFileInfo(e.Data)
}

// parseFixedFileInfo locates the VS_FIXEDFILEINFO signature within a version blob
// and reads dwFileVersionMS/LS.
func parseFixedFileInfo(blob []byte) (fileVersion, error) {
	var sig [4]byte
	binary.LittleEndian.PutUint32(sig[:], vsFixedFileInfoSig)
	i := bytes.Index(blob, sig[:])
	if i < 0 || i+16 > len(blob) {
		return fileVersion{}, fmt.Errorf("VS_FIXEDFILEINFO not found")
	}
	// From the signature: dwSignature, dwStrucVersion, dwFileVersionMS,
	// dwFileVersionLS, ...
	ms := binary.LittleEndian.Uint32(blob[i+8:])
	ls := binary.LittleEndian.Uint32(blob[i+12:])
	return fileVersion{
		Major:    int(ms >> 16),
		Minor:    int(ms & 0xffff),
		Build:    int(ls >> 16),
		Revision: int(ls & 0xffff),
	}, nil
}
