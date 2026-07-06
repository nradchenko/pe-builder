// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"fmt"
	"io/fs"

	"github.com/nradchenko/pe-builder/compress"
)

// minVersion is the lowest source version accepted: Windows XP SP1
// (5.1.2600.1045). Older media predates the PE-mode build features this tool
// relies on.
var minVersion = fileVersion{Major: 5, Minor: 1, Build: 2600, Revision: 1045}

// archFolders maps a source's top-level architecture folder to the Arch value.
// amd64 is checked first: x64 install media carries BOTH an AMD64 folder (the
// 64-bit binaries) and an I386 folder (32-bit setup files), so an AMD64 folder is
// the decisive signal, while 32-bit media has only I386.
var archFolders = []struct{ folder, arch string }{
	{"amd64", "amd64"},
	{"i386", "i386"},
}

// flavors maps a build number to its flavor name.
var flavors = map[int]string{
	2600: "winxp",
	3790: "win2003",
}

// Probe detects the source's identity: architecture from the source folder, and
// build/version from ntdll's PE version resource (decompressing the .??_ archive
// when ntdll ships compressed).
func (s *source) Probe() (Identity, error) {
	folder, arch, err := s.archFolder()
	if err != nil {
		return Identity{}, err
	}

	ntdll, err := s.readNtdll(folder)
	if err != nil {
		return Identity{}, err
	}
	ver, err := peFileVersion(ntdll)
	if err != nil {
		return Identity{}, fmt.Errorf("read ntdll version: %w", err)
	}
	if !ver.atLeast(minVersion) {
		return Identity{}, fmt.Errorf("source version %s is older than the minimum supported %s",
			ver, minVersion)
	}

	return Identity{
		Build:  ver.Build,
		Arch:   arch,
		Flavor: flavors[ver.Build], // empty for a supported-but-unclassified build
	}, nil
}

// archFolder finds the source's architecture folder and its Arch value.
func (s *source) archFolder() (folder, arch string, err error) {
	for _, a := range archFolders {
		if info, err := fs.Stat(s.backing, a.folder); err == nil && info.IsDir() {
			return a.folder, a.arch, nil
		}
	}
	return "", "", fmt.Errorf("no source architecture folder (i386/amd64) found")
}

// readNtdll returns ntdll's PE bytes, reading the loose uncompressed ntdll.dll if
// present or decompressing the ntdll.dl_ cabinet otherwise.
func (s *source) readNtdll(folder string) ([]byte, error) {
	if data, err := fs.ReadFile(s.backing, folder+"/ntdll.dll"); err == nil {
		return data, nil
	}
	packed, err := fs.ReadFile(s.backing, folder+"/ntdll.dl_")
	if err != nil {
		return nil, fmt.Errorf("read ntdll (ntdll.dll/ntdll.dl_) from %s: %w", folder, err)
	}
	data, err := compress.DecompressBytes(packed)
	if err != nil {
		return nil, fmt.Errorf("decompress ntdll.dl_: %w", err)
	}
	return data, nil
}
