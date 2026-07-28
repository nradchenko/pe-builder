// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package receipt

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func marshalled(t *testing.T) []byte {
	t.Helper()
	data, err := sample().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The tree writes the receipt lowercase and the media stage uppercases the whole tree on
// the way to the disc, so both spellings have to read back.
func TestReadFSBothSpellings(t *testing.T) {
	data := marshalled(t)
	for _, name := range []string{FileName, strings.ToUpper(FileName)} {
		t.Run(name, func(t *testing.T) {
			r, err := ReadFS(fstest.MapFS{name: {Data: data}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Manifest.Digest != "sha256:bb" {
				t.Errorf("read the wrong receipt: %+v", r.Manifest)
			}
		})
	}
}

// An image with no receipt is its own outcome, not a mismatch: it was built before
// receipts existed, or not by pebuild at all, and a caller has to be able to tell those
// apart from "built by a different pebuild".
func TestReadFSNotFound(t *testing.T) {
	_, err := ReadFS(fstest.MapFS{"i386/setupldr.bin": {Data: []byte("x")}})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A receipt that is present but unparsable is a third outcome again — reporting it as
// absent would say the image predates receipts, which is a different claim.
func TestReadFSUnparsable(t *testing.T) {
	_, err := ReadFS(fstest.MapFS{FileName: {Data: []byte("{ not json")}})
	if err == nil {
		t.Fatal("an unparsable receipt read without error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("an unparsable receipt was reported as absent: %v", err)
	}
}

// Nor may a receipt that exists but cannot be read pass as absent: a permission error, a
// truncated image or an I/O failure says nothing about how the image was built, and
// "built before receipts existed, or not by pebuild" is a claim that it does not support.
func TestReadFSUnreadableIsNotAbsent(t *testing.T) {
	_, err := ReadFS(errFS{fstest.MapFS{FileName: {Data: marshalled(t)}}})
	if err == nil {
		t.Fatal("an unreadable receipt read without error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("an unreadable receipt was reported as absent: %v", err)
	}
	if !errors.Is(err, errRefused) {
		t.Errorf("the underlying failure was lost: %v", err)
	}
}

var errRefused = errors.New("refused")

// errFS fails every open with something other than "does not exist" — the I/O failure a
// real image can hand back.
type errFS struct{ fs.FS }

func (errFS) Open(string) (fs.File, error) { return nil, errRefused }
