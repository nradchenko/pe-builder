// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package build

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/nradchenko/pe-builder/manifest"
	"github.com/nradchenko/pe-builder/plugin"
	"github.com/nradchenko/pe-builder/receipt"
	"github.com/nradchenko/pe-builder/source"
)

// Status is the verdict on one recomputed field.
type Status int

const (
	// OK means the field matches what a build from the current inputs would record.
	OK Status = iota
	// Changed means the field differs — this image was not built from these inputs.
	Changed
	// Unknown means the field could not be recomputed (typically a source that is no
	// longer where the manifest says). It is not a match, and not a mismatch either;
	// saying either would overstate what was actually checked.
	Unknown
)

func (s Status) String() string {
	switch s {
	case OK:
		return "OK"
	case Changed:
		return "CHANGED"
	default:
		return "UNKNOWN"
	}
}

// Field is one line of a verification: what the image recorded, what the current inputs
// produce, and the verdict. Informational fields carry no verdict.
type Field struct {
	Name string
	// Value is what is shown for the field — the current value when there is one, else
	// the recorded one.
	Value string
	// Recorded is shown alongside a mismatch, so the report says what the image claims
	// as well as what it should have claimed.
	Recorded string
	Status   Status
	// Informational marks a field that is reported but does not decide the verdict.
	Informational bool
	// Detail carries per-item specifics for a composite field (which plugin, which
	// option), so a mismatch names what differs instead of only that something does.
	Detail []string
}

// Verification is the result of checking an image's receipt against the current inputs.
type Verification struct {
	Fields []Field
	// Recorded is the receipt read out of the image.
	Recorded *receipt.Receipt
}

// Match reports whether every graded field matched. It is false when any field could not
// be checked, so "verified" never means "mostly verified".
func (v *Verification) Match() bool {
	for _, f := range v.Fields {
		if !f.Informational && f.Status != OK {
			return false
		}
	}
	return true
}

// Verify recomputes what a build from the given manifest would record now and compares
// it, field by field, with the receipt read out of an image.
//
// The recomputation runs through makeReceipt — the same function a build writes its
// receipt with — so the two cannot drift apart into disagreeing about what a resolved
// build set is. Nothing is built and nothing is written; the source is opened only to
// probe its identity and to resolve the plugin set the way a build would.
func Verify(m *manifest.Manifest, rec *receipt.Receipt) (*Verification, error) {
	v := &Verification{Recorded: rec}

	// The engine and the manifest are checkable with no source at all, so they are
	// compared first and stay meaningful when the media has since moved away.
	current, known, err := currentReceipt(m)
	if err != nil {
		return nil, err
	}

	v.Fields = append(v.Fields, Field{
		Name:          "pebuild version",
		Value:         current.PEBuild.Version,
		Recorded:      rec.PEBuild.Version,
		Informational: true,
		// A version string flags a dirty tree but does not characterize it: two
		// different sets of uncommitted edits give the same string. The engine hash
		// below is what actually answers the question, so the version is reported and
		// left ungraded rather than failing a build that is materially identical.
	})
	v.Fields = append(v.Fields, compare("engine content",
		rec.PEBuild.EngineHash, current.PEBuild.EngineHash))
	v.Fields = append(v.Fields, compare("manifest",
		rec.Manifest.Digest, current.Manifest.Digest))
	v.Fields = append(v.Fields, sourceField(rec.Source, current.Source, known.source))
	v.Fields = append(v.Fields, pluginsField(rec.Plugins, current.Plugins, known.plugins))
	v.Fields = append(v.Fields, optionsField(rec.Plugins, current.Plugins, known.plugins))
	return v, nil
}

// currentReceipt builds the receipt a build from this manifest would write now, and
// reports whether the plugin set could be resolved at all.
//
// Neither an unreadable source nor an unresolvable plugin set is fatal here — the engine
// and manifest are still worth reporting, and verifying an image away from the tree that
// built it is the case this command exists for, where a relative plugins_dir or a
// drop-in plugin may well be absent. Both degrade to resolved=false, which surfaces as
// UNKNOWN on the fields that need them. Recomputing those against an empty set would read
// as "the plugins changed", which is a claim about the image rather than about what could
// be checked; refusing to report at all would answer nothing.
//
// The source gates plugin resolution because a plugin scopes its sections by
// architecture: resolving one without knowing the target would answer a different
// question than the build asked.
func currentReceipt(m *manifest.Manifest) (rec *receipt.Receipt, known checked, err error) {
	var (
		id       source.Identity
		selected []plugin.Selected
	)
	if src, openErr := source.Open(m.Source.Path); openErr == nil {
		if probed, probeErr := src.Probe(); probeErr == nil {
			id = probed
			known.source = true
			if sel, selErr := selectPlugins(m, id); selErr == nil {
				selected, known.plugins = sel, true
			} else {
				slog.Debug("verify: cannot resolve the plugin set", "error", selErr)
			}
		}
	}
	rec, err = makeReceipt(m, id, selected, time.Now())
	return rec, known, err
}

// checked records which inputs could actually be recomputed. They are tracked apart
// because they fail apart: a plugin directory that has since moved says nothing about
// whether the Windows source is still readable, and marking the source UNKNOWN over an
// unrelated plugin failure would understate what was verified.
type checked struct {
	source  bool
	plugins bool
}

// compare grades one scalar field. An empty recorded value means the receipt does not
// carry it, which cannot be compared and so is not claimed as a match.
func compare(name, recorded, current string) Field {
	f := Field{Name: name, Value: current, Recorded: recorded}
	switch {
	case recorded == "" || current == "":
		f.Status = Unknown
	case recorded == current:
		f.Status = OK
	default:
		f.Status = Changed
	}
	return f
}

// sourceField grades the Windows source: its identity, size and mtime together. The
// receipt fingerprints the media rather than hashing it, so this catches a swap to
// different media and knowingly not an edit that preserved size and timestamp.
func sourceField(rec, cur receipt.Source, readable bool) Field {
	f := Field{Name: "source", Value: cur.Identity, Recorded: rec.Identity}
	if !readable {
		// The manifest's source is gone or unreadable, so nothing here was checked.
		// This keys off whether the source could actually be probed rather than off the
		// recomputed identity: an unprobed Identity is not empty, it renders as
		// "build 0 ()", which would otherwise be compared and reported as a difference —
		// accusing the image of a change on the strength of a value never obtained.
		f.Status, f.Value = Unknown, rec.Identity
		f.Detail = append(f.Detail, "the manifest's source could not be read")
		return f
	}
	f.Status = OK
	if rec.Identity != cur.Identity {
		f.Status = Changed
		f.Detail = append(f.Detail, fmt.Sprintf("identity: built with %s", rec.Identity))
	}
	if rec.File != cur.File {
		f.Status = Changed
		f.Detail = append(f.Detail, fmt.Sprintf("file: built from %s, now %s", rec.File, cur.File))
	}
	if rec.Size != cur.Size {
		f.Status = Changed
		f.Detail = append(f.Detail, fmt.Sprintf("size: built from %d bytes, now %d", rec.Size, cur.Size))
	}
	if !rec.MTime.Equal(cur.MTime) {
		f.Status = Changed
		f.Detail = append(f.Detail, fmt.Sprintf("mtime: built from %s, now %s",
			rec.MTime.Format(time.RFC3339), cur.MTime.Format(time.RFC3339)))
	}
	return f
}

// pluginsField grades the resolved plugin set: the same plugins, in the same order, from
// the same origin, with an out-of-tree plugin's content unchanged.
func pluginsField(rec, cur []receipt.Plugin, resolved bool) Field {
	f := Field{
		Name:     "plugins",
		Value:    fmt.Sprintf("%d resolved", len(cur)),
		Recorded: fmt.Sprintf("%d resolved", len(rec)),
		Status:   OK,
	}
	if !resolved {
		f.Status, f.Value = Unknown, fmt.Sprintf("%d recorded", len(rec))
		f.Detail = append(f.Detail, "the plugin set could not be resolved (the source or a plugin directory is unavailable); re-run with -v for the reason")
		return f
	}
	recByName := byName(rec)
	curByName := byName(cur)
	for _, name := range sortedNames(recByName, curByName) {
		r, inRec := recByName[name]
		c, inCur := curByName[name]
		switch {
		case !inCur:
			f.Status = Changed
			f.Detail = append(f.Detail, fmt.Sprintf("%s: was built in, no longer resolved", name))
		case !inRec:
			f.Status = Changed
			f.Detail = append(f.Detail, fmt.Sprintf("%s: now resolved, was not built in", name))
		case r.Origin != c.Origin:
			f.Status = Changed
			f.Detail = append(f.Detail, fmt.Sprintf("%s: built from %s, now %s", name, r.Origin, c.Origin))
		case r.ContentHash != c.ContentHash:
			// Only an out-of-tree plugin carries one; a built-in's content is covered by
			// the engine hash instead.
			f.Status = Changed
			f.Detail = append(f.Detail, fmt.Sprintf("%s: contents changed", name))
		}
	}
	// Order is part of the answer: plugins apply last-writer-wins, so the same set in a
	// different order is a different build.
	if f.Status == OK && !sameOrder(rec, cur) {
		f.Status = Changed
		f.Detail = append(f.Detail, "the same plugins resolved in a different order")
	}
	return f
}

// optionsField grades the effective option values, naming each one that differs — the
// question this makes answerable without booting ("was this built at 1280x1024?").
func optionsField(rec, cur []receipt.Plugin, resolved bool) Field {
	f := Field{Name: "options", Status: OK}
	if !resolved {
		// Says why, like the fields above: an UNKNOWN with no reason reads as a defect
		// in the tool rather than as a fact about what could be checked.
		f.Status, f.Value = Unknown, "not resolved"
		f.Detail = append(f.Detail, "option values come from the resolved plugin set, which is unavailable")
		return f
	}
	recByName := byName(rec)
	curByName := byName(cur)

	var shown []string
	for _, name := range sortedNames(recByName, curByName) {
		r, inRec := recByName[name]
		c, inCur := curByName[name]
		if !inRec || !inCur {
			continue // already reported as a plugin-set difference
		}
		for _, k := range sortedKeys(r.Options, c.Options) {
			rv, inRecOpts := r.Options[k]
			cv, inCurOpts := c.Options[k]
			switch {
			case !inRecOpts:
				// The plugin has gained an option since. Saying it was "built with
				// nothing" would claim the build chose an empty value, when in truth
				// there was no such knob to choose.
				f.Status = Changed
				f.Detail = append(f.Detail, fmt.Sprintf(
					"%s.%s: not declared when this was built, now %q", name, k, cv))
			case !inCurOpts:
				f.Status = Changed
				f.Detail = append(f.Detail, fmt.Sprintf(
					"%s.%s: built with %q, no longer declared", name, k, rv))
			case rv != cv:
				f.Status = Changed
				f.Detail = append(f.Detail, fmt.Sprintf("%s.%s: built with %q, now %q", name, k, rv, cv))
			default:
				shown = append(shown, fmt.Sprintf("%s.%s=%s", name, k, cv))
			}
		}
	}
	switch {
	case len(shown)+len(f.Detail) == 0:
		f.Value = "none declared"
	case f.Status == OK:
		f.Value = shown[0]
		if len(shown) > 1 {
			f.Value += fmt.Sprintf(" (+%d more)", len(shown)-1)
		}
	default:
		f.Value = fmt.Sprintf("%d differ", len(f.Detail))
	}
	return f
}

func byName(ps []receipt.Plugin) map[string]receipt.Plugin {
	out := make(map[string]receipt.Plugin, len(ps))
	for _, p := range ps {
		out[strings.ToLower(p.Name)] = p
	}
	return out
}

func sortedNames(a, b map[string]receipt.Plugin) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, m := range []map[string]receipt.Plugin{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(a, b map[string]string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sameOrder reports whether two resolved sets list the same plugins in the same order.
func sameOrder(a, b []receipt.Plugin) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i].Name, b[i].Name) {
			return false
		}
	}
	return true
}
