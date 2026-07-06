// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package hive

import (
	"fmt"
	"io/fs"

	"github.com/nradchenko/pe-builder/source"
)

// Hives are the four base registry hives, built from the source's own material
// before any base-manifest or plugin registry actions are applied.
type Hives struct {
	System   *Hive // from the pre-built setupreg.hiv
	Software *Hive // HKLM\SOFTWARE, from hivesft.inf
	Default  *Hive // the default user hive, from hivedef.inf
	Classes  *Hive // HKCR, from hivecls.inf
}

// baseINF describes how one hive-population INF maps onto a fresh hive tree: the
// reg-root token whose lines populate it and the subkey prefix that corresponds
// to the hive's root (and so is stripped from each path).
type baseINF struct {
	name      string // source i386 file name
	rootName  string // embedded root-key name for the fresh hive
	wantRoot  string // reg-root token to accept
	stripHead string // subkey prefix mapped onto the hive root
}

// The CLASSES tree is built standalone here (HKCR maps to its root). On a real
// XP system machine classes live under HKLM\SOFTWARE\Classes rather than in a
// separate hive file; grafting this tree there is an integration-time decision,
// left until the hives are assembled into the output config. The lone
// HKLM,"SOFTWARE\Classes" anchor line in hivecls.inf is not an HKCR line and is
// ignored while building the standalone tree.
var (
	softwareINF = baseINF{"hivesft.inf", "$$$PROTO.HIV", "HKLM", "SOFTWARE"}
	defaultINF  = baseINF{"hivedef.inf", "$$$PROTO.HIV", "HKCU", ""}
	classesINF  = baseINF{"hivecls.inf", "$$$PROTO.HIV", "HKCR", ""}
)

// BaseHives builds the four base hives from a Windows installation source: SYSTEM
// is read from the pre-built setupreg.hiv; SOFTWARE, DEFAULT, and CLASSES start
// empty and are populated from hivesft.inf, hivedef.inf, and hivecls.inf.
func BaseHives(b source.Backing) (*Hives, error) {
	sysData, err := readI386(b, "setupreg.hiv")
	if err != nil {
		return nil, err
	}
	system, err := ReadHive(sysData)
	if err != nil {
		return nil, fmt.Errorf("hive: read setupreg.hiv: %w", err)
	}

	software, err := buildFromINF(b, softwareINF)
	if err != nil {
		return nil, err
	}
	def, err := buildFromINF(b, defaultINF)
	if err != nil {
		return nil, err
	}
	classes, err := buildFromINF(b, classesINF)
	if err != nil {
		return nil, err
	}

	return &Hives{System: system, Software: software, Default: def, Classes: classes}, nil
}

// buildFromINF creates a fresh hive and populates it from its hive-population INF.
func buildFromINF(b source.Backing, cfg baseINF) (*Hive, error) {
	data, err := readI386(b, cfg.name)
	if err != nil {
		return nil, err
	}
	h := NewHive(cfg.rootName)
	if err := ApplyHiveINF(h.Root, data, cfg.wantRoot, cfg.stripHead); err != nil {
		return nil, fmt.Errorf("hive: process %s: %w", cfg.name, err)
	}
	return h, nil
}

// readI386 reads a file from the source's i386 directory.
func readI386(b source.Backing, name string) ([]byte, error) {
	data, err := fs.ReadFile(b, "i386/"+name)
	if err != nil {
		return nil, fmt.Errorf("hive: read i386/%s: %w", name, err)
	}
	return data, nil
}
