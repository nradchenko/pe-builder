// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"bytes"
	"io"
	"os"
)

// The image formats a disk can be attached in.
const (
	// FormatRaw is a plain sector image — what this package writes, and what anything
	// that cannot be identified as something else is assumed to be.
	FormatRaw = "raw"
	// FormatQCOW2 is QEMU's own copy-on-write format. It can be attached to a boot but
	// not read by this package.
	FormatQCOW2 = "qcow2"
)

// qcow2Magic is the signature at the start of a QCOW image: "QFI" and a trailing 0xfb.
var qcow2Magic = []byte{'Q', 'F', 'I', 0xfb}

// containers are the disk-image formats this package does not read, recognised so that one
// can be named rather than mistaken for a raw image.
//
// The point is not to support them. It is that "not something I recognise" and "a raw sector
// image" are different answers, and returning raw for both is the one that does damage: a
// caller asserting format=raw to an emulator hands the guest the container's own header where
// its partition table should be, and the guest boots from garbage with nothing reported. A
// name here turns that into a refusal that says what the file is.
//
// Magic at offset 0 only, which is what makes this list short and certain. A fixed-size VHD
// carries its footer at the *end* and is otherwise a raw image byte for byte, so it is not
// here and is treated as raw — which is very nearly true and boots.
//
// The names are QEMU's driver names rather than the friendly ones, because that is where the
// value goes: onto a command line as format=, and into the qemu-img -f of the message telling
// a caller how to convert. QEMU's name for VHD is "vpc", so that is what a .vhd reports.
var containers = []struct {
	magic []byte
	name  string
}{
	{qcow2Magic, FormatQCOW2},
	{[]byte("KDMV"), "vmdk"},
	{[]byte("<<< Oracle VM VirtualBox Disk Image >>>"), "vdi"},
	{[]byte("vhdxfile"), "vhdx"},
	{[]byte("conectix"), "vpc"}, // dynamic/differencing VHD; a fixed one has this only at the end
	{[]byte("QED\x00"), "qed"},
}

// SniffFormat reports the image format of a file from its own bytes rather than from its
// name.
//
// Content decides here for the same reason it decides elsewhere in the tool — an image is
// recognised by its ISO9660 descriptor and a registry hive by its regf magic, whatever
// either is called. A disk named .img that is really a qcow2, or a raw image with no
// extension at all, is then read as what it is.
//
// A file too short to hold the signature is raw, not an error: being short is an ordinary
// way for a raw image to answer no, and a genuinely unreadable file fails at Open with a
// message about the real problem.
func SniffFormat(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// One read long enough for the longest signature; a shorter file is read as far as it
	// goes and compared on what it has.
	head := make([]byte, longestMagic())
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	head = head[:n]

	for _, c := range containers {
		if len(head) >= len(c.magic) && bytes.Equal(head[:len(c.magic)], c.magic) {
			return c.name, nil
		}
	}
	return FormatRaw, nil
}

func longestMagic() int {
	n := 0
	for _, c := range containers {
		if len(c.magic) > n {
			n = len(c.magic)
		}
	}
	return n
}
