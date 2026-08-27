// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"fmt"
	"strconv"
	"strings"
)

// Size suffixes, in bytes. They are binary throughout: 256M is 256 MiB, which is what
// anyone typing a disk size means and what every tool that makes one already does. The
// decimal reading is not offered, because the only thing distinguishing it would buy is a
// way to land on a surprising cluster count.
const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

// ParseSize reads a disk size: a whole number of bytes, or a number with a K, M or G
// suffix (KB/MB/GB and KiB/MiB/GiB are accepted as the same thing). Case does not matter.
//
// It lives here rather than beside a flag so that every caller that takes a size means
// the same thing by one — a command that creates a disk and a command that attaches one
// cannot disagree about what 256M is.
func ParseSize(s string) (int64, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return 0, fmt.Errorf("no size given")
	}

	digits := strings.TrimRight(text, "bBkKmMgGiI")
	suffix := text[len(digits):]

	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size; give a number of bytes or a size like 256M", s)
	}

	var unit int64
	switch strings.ToLower(suffix) {
	case "", "b":
		unit = 1
	case "k", "kb", "kib":
		unit = kib
	case "m", "mb", "mib":
		unit = mib
	case "g", "gb", "gib":
		unit = gib
	default:
		return 0, fmt.Errorf("%q has an unknown size suffix %q; use K, M or G", s, suffix)
	}

	if n <= 0 {
		return 0, fmt.Errorf("%q is not a usable size", s)
	}
	// Reject an overflow rather than wrapping to a negative size that would be reported
	// as a nonsense number much further down.
	if n > (1<<63-1)/unit {
		return 0, fmt.Errorf("%q is too large to be a disk size", s)
	}
	return n * unit, nil
}

// FormatSize renders a byte count the way a size is reported back to a user: in the
// largest binary unit that divides it exactly, so a volume comes out as "255 MiB" rather
// than a bare count of bytes.
//
// An exact smaller unit is preferred over a rounded larger one — 3584 MiB rather than
// 3.5 GiB. That reads less naturally, and is the right trade: this renders the size a
// caller prints for a disk it has attached, and a volume a megabyte under 2 GiB reported
// as "2.0 GiB" would be the status line quietly overstating what the guest can use.
func FormatSize(n int64) string {
	switch {
	case n >= gib && n%gib == 0:
		return fmt.Sprintf("%d GiB", n/gib)
	case n >= mib && n%mib == 0:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= kib && n%kib == 0:
		return fmt.Sprintf("%d KiB", n/kib)
	}
	// Not a round number of units — a volume is a disk minus its alignment gap, so this
	// is the common case for one. Report it to one decimal place in the largest unit that
	// fits, which is precise enough to compare two disks and short enough to read.
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	}
	return fmt.Sprintf("%d bytes", n)
}
