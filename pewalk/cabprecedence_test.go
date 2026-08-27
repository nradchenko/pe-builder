// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package pewalk

import (
	"bytes"
	"testing"
	"testing/fstest"
)

// A medium may hold the same member in more than one cabinet: a slipstreamed source keeps
// SP2.CAB beside SP3.CAB, and the two copies are different builds of the same file. Which
// one the index names is not cosmetic — it is the copy the import closure is computed from,
// while the tree stages the copy internal/tree picks, and the two reading different builds
// of one binary is how a build ships something whose dependencies were never staged. It
// fails at boot as a missing DLL, named against a file that is present.
//
// The rule is precedence, not discovery order. Taking whichever cabinet the walk reached
// first takes the OLDEST, the walk being lexical — which is exactly what these check.

// twoCabinets is a source holding one member name in both cabinets, with contents that say
// which cabinet each came from.
func twoCabinets(t *testing.T, member string) (fstest.MapFS, []byte, []byte) {
	t.Helper()
	sp2 := bytes.Repeat([]byte{0x22}, 300)
	sp3 := bytes.Repeat([]byte{0x33}, 300)
	return fstest.MapFS{
		"i386/sp2.cab": {Data: writeCabinet([]cabMember{{name: member, data: sp2}})},
		"i386/sp3.cab": {Data: writeCabinet([]cabMember{{name: member, data: sp3}})},
	}, sp2, sp3
}

// TestCabinetPrecedenceTakesNewestServicePack is the regression test: SP3 wins over SP2
// although the lexical walk reaches SP2 first.
func TestCabinetPrecedenceTakesNewestServicePack(t *testing.T) {
	fsys, _, sp3 := twoCabinets(t, "shared.dll")

	s, err := Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := s.Locate("shared.dll")
	if !ok {
		t.Fatal("shared.dll not located")
	}
	if o.Path != "i386/sp3.cab" {
		t.Errorf("located in %s, want i386/sp3.cab — the newer service pack must win, "+
			"and the lexical walk reaches sp2.cab first", o.Path)
	}
	got, _, err := s.Read("shared.dll")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, sp3) {
		t.Errorf("read the copy from the wrong cabinet: the bytes are SP2's")
	}
}

// TestCabinetPrecedenceServicePackBeatsDriverCab covers the ordering the comment has always
// claimed and nothing checked: a service-pack cabinet supersedes the base DRIVER.CAB, which
// the lexical walk would also get backwards (driver.cab sorts before sp3.cab).
func TestCabinetPrecedenceServicePackBeatsDriverCab(t *testing.T) {
	member := bytes.Repeat([]byte{0x44}, 200)
	fsys := fstest.MapFS{
		"i386/driver.cab": {Data: writeCabinet([]cabMember{{name: "both.sys", data: bytes.Repeat([]byte{0x11}, 200)}})},
		"i386/sp3.cab":    {Data: writeCabinet([]cabMember{{name: "both.sys", data: member}})},
	}
	s, err := Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := s.Locate("both.sys")
	if !ok {
		t.Fatal("both.sys not located")
	}
	if o.Path != "i386/sp3.cab" {
		t.Errorf("located in %s, want i386/sp3.cab", o.Path)
	}
}

// TestCabinetPrecedenceLooseStillWins pins the rule ranking does not disturb: a loose copy
// is the slipstreamed, current file and outranks every cabinet.
func TestCabinetPrecedenceLooseStillWins(t *testing.T) {
	loose := bytes.Repeat([]byte{0x55}, 100)
	fsys, _, _ := twoCabinets(t, "shared.dll")
	fsys["i386/shared.dll"] = &fstest.MapFile{Data: loose}

	s, err := Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := s.Locate("shared.dll")
	if !ok {
		t.Fatal("shared.dll not located")
	}
	if o.Kind != OriginLoose {
		t.Errorf("located as %v in %s, want the loose copy", o.Kind, o.Path)
	}
}

// TestCabinetRankUnlistedRanksLast keeps an unrecognised cabinet from displacing a named
// one. Ranking equal, the first walked wins, so the choice stays deterministic.
func TestCabinetRankUnlistedRanksLast(t *testing.T) {
	if cabRank("i386/sp3.cab") >= cabRank("i386/mystery.cab") {
		t.Errorf("a named cabinet must outrank an unlisted one")
	}
	if cabRank("i386/mystery.cab") != cabRank("i386/other.cab") {
		t.Errorf("two unlisted cabinets must rank equally")
	}
	// Case does not decide precedence: media name these in upper case.
	if cabRank("I386/SP3.CAB") != cabRank("i386/sp3.cab") {
		t.Errorf("cabinet rank must not depend on case")
	}
}

// TestCabinetOrderIsACopy guards the exported list, which internal/tree iterates: handing
// out the backing array would let a caller reorder resolution for everyone.
func TestCabinetOrderIsACopy(t *testing.T) {
	got := CabinetOrder()
	if len(got) == 0 {
		t.Fatal("CabinetOrder is empty")
	}
	first := got[0]
	got[0] = "tampered.cab"
	if CabinetOrder()[0] != first {
		t.Errorf("CabinetOrder handed out its backing array; a caller can reorder precedence")
	}
}
