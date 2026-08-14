// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"fmt"
	"io/fs"

	"github.com/nradchenko/pe-builder/compress"
)

// minVersion is the lowest source version accepted: Windows XP SP3
// (5.1.2600.5512, the revision SP3 media ships ntdll at). SP3 was the last XP
// service pack and supersedes the earlier ones, so building from SP0/SP1/SP2
// media has almost no practical use and is not a target; such a source is
// refused here rather than failing somewhere deeper in the pipeline.
var minVersion = fileVersion{Major: 5, Minor: 1, Build: 2600, Revision: 5512}

// The architectures Probe can detect, and so the only values Identity.Arch takes.
// They are exported because a caller that selects behaviour per architecture — which
// emulator boots an image, which binaries a toolchain builds — would otherwise
// restate these strings, and drift from them when a third architecture appears.
const (
	ArchI386  = "i386"
	ArchAMD64 = "amd64"
)

// archFolders maps a source's top-level architecture folder to the Arch value.
// amd64 is checked first: x64 install media carries BOTH an AMD64 folder (the
// 64-bit binaries) and an I386 folder (32-bit setup files), so an AMD64 folder is
// the decisive signal, while 32-bit media has only I386.
var archFolders = []struct{ folder, arch string }{
	{ArchAMD64, ArchAMD64},
	{ArchI386, ArchI386},
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

	return Identity{Build: ver.Build, Arch: arch}, nil
}

// archFolder finds the source's architecture folder and its Arch value.
func (s *source) archFolder() (folder, arch string, err error) {
	folder, arch, ok := detectArchFolder(s.backing)
	if !ok {
		return "", "", fmt.Errorf("no source architecture folder (i386/amd64) found")
	}
	return folder, arch, nil
}

// DetectArch reports a tree's architecture from its top-level folders alone, and
// whether one was found.
//
// It answers the architecture question for a tree Probe cannot answer it for — a
// *built* PE, which keeps the source's folder names but no longer carries the ntdll
// archive Probe reads a version from. The folder is the same decisive signal Probe
// uses, so the two cannot disagree about what an amd64 tree looks like; what a caller
// gives up by asking here is the build number, which only a source has.
func DetectArch(b Backing) (arch string, ok bool) {
	_, arch, ok = detectArchFolder(b)
	return arch, ok
}

// detectArchFolder scans a backing for the first architecture folder in archFolders
// order, so the source probe and a built tree's reader look the same way.
func detectArchFolder(b Backing) (folder, arch string, ok bool) {
	for _, a := range archFolders {
		if info, err := fs.Stat(b, a.folder); err == nil && info.IsDir() {
			return a.folder, a.arch, true
		}
	}
	return "", "", false
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
