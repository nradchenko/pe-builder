// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package inf

import (
	"strings"
	"testing"
)

// A miniature stand-in for a network-component INF, carrying one of each construct the
// transform touches and several it must leave alone. The section shapes mirror nettcpip.inf's:
// a `.Secure` section creates keys and its `.Security` companion holds the descriptor for them.
const sampleINF = `; a component INF
[Version]
Signature = "$Windows NT$"
Class = NetTrans

[MS_Sample.PrimaryInstall]
Characteristics = 0xA0
AddReg = Registry.Sample.PrimaryInstall, Registry.Sample

[Registry.Sample.PrimaryInstall]
HKR,Ndi,Service,,Sample
HKR,Ndi,CoServices,0x00010000,"Sample","Helper","Other"
HKR,Ndi,ExcludeSetupStartServices,0x00010000,"Other"

[MS_Sample.PrimaryInstall.Services]
AddService = Sample, 0, Install.AddService.Sample

[Install.AddService.Sample]
DisplayName = Sample
ServiceBinary = %12%\sample.sys
AddReg = Registry.Service.Sample.Parameters.Secure, Registry.Service.Sample.Secure, Registry.Service.Sample
Security = D:(A;;CCLCSWLOCRRC;;;AU)

[Install.AddService.Only]
AddReg = Registry.Service.Only.Security
Security = D:(A;;0x2019D;;;AU)

[Registry.Service.Sample.Secure]
HKR,Linkage,,0x00000010

[Registry.Service.Sample.Secure.Security]
D:P(A;CI;GR;;;BU)(A;CI;GA;;;BA)

[Registry.Service.Sample.Parameters.Secure]
HKR,Parameters\PersistentRoutes,,0x00000010

[Registry.Service.Sample.Parameters.Secure.Security]
D:P(A;CI;GR;;;BU)(A;CI;GA;;;BA)

[Registry.Service.Only.Security]
D:P(A;CI;GA;;;BA)

[Registry.Service.Sample]
HKR,Parameters,Kept,0x00010001,1
`

func TestAdaptNetworkComponent(t *testing.T) {
	got := string(AdaptNetworkComponent([]byte(sampleINF)))

	// Both forms of descriptor go: the directive, and the section whose body is one.
	if strings.Contains(got, "Security = D:") {
		t.Error("a Security= directive survived")
	}
	for _, s := range []string{
		"[Registry.Service.Sample.Secure.Security]",
		"[Registry.Service.Sample.Parameters.Secure.Security]",
		"[Registry.Service.Only.Security]",
	} {
		if strings.Contains(got, s) {
			t.Errorf("descriptor section %s survived", s)
		}
	}
	// Their contents go with them — a dropped header that left its body behind would silently
	// merge those lines into the preceding section, which is worse than not dropping it.
	if strings.Contains(got, "D:P(A;CI;") {
		t.Error("a descriptor section's body survived its header")
	}

	// The key-creating sections beside them stay, and so do the keys. Dropping these was the
	// bug: nothing else in the file creates PersistentRoutes, so a route had nowhere to go.
	for _, s := range []string{
		"[Registry.Service.Sample.Secure]",
		"[Registry.Service.Sample.Parameters.Secure]",
		`HKR,Parameters\PersistentRoutes,,0x00000010`,
		"HKR,Linkage,,0x00000010",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("a key-creating section was dropped with its descriptor: %q", s)
		}
	}
	// So the AddReg naming them has nothing to lose, and is passed through untouched.
	if !strings.Contains(got, "AddReg = Registry.Service.Sample.Parameters.Secure, Registry.Service.Sample.Secure, Registry.Service.Sample\n") {
		t.Errorf("an AddReg with nothing to drop was rewritten:\n%s", got)
	}
	// An AddReg naming only a dropped section goes rather than being left empty.
	if strings.Contains(got, "AddReg = Registry.Service.Only.Security") ||
		strings.Contains(got, "AddReg = \n") {
		t.Error("an AddReg left with no sections was not dropped")
	}

	// ExcludeSetupStartServices takes the CoServices list, one space after the flags comma.
	if !strings.Contains(got, `HKR,Ndi,ExcludeSetupStartServices,0x00010000, "Sample","Helper","Other"`) {
		t.Errorf("ExcludeSetupStartServices was not widened to CoServices:\n%s", got)
	}
	// CoServices itself is untouched.
	if !strings.Contains(got, `HKR,Ndi,CoServices,0x00010000,"Sample","Helper","Other"`) {
		t.Error("CoServices was modified")
	}

	// Everything else is left alone.
	for _, s := range []string{
		"Signature = \"$Windows NT$\"",
		"Class = NetTrans",
		"Characteristics = 0xA0",
		"AddService = Sample, 0, Install.AddService.Sample",
		`ServiceBinary = %12%\sample.sys`,
		"HKR,Parameters,Kept,0x00010001,1",
		"[Install.AddService.Sample]",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("unrelated line was lost: %q", s)
		}
	}
}

// A section header may legally carry a trailing comment, and a section list may end in one.
// Both are one comment away from silent damage: an unrecognised header leaves a drop running
// past the section it should have ended, and an unrecognised suffix leaves a reference pointing
// at a section that has been removed.
func TestAdaptNetworkComponentHandlesTrailingComments(t *testing.T) {
	const src = `[Install.Sample]
AddReg = Registry.Sample.Security, Registry.Sample ; two of them
Security = D:(A;;CCLCSWLOCRRC;;;AU)

[Registry.Sample.Security] ; dropped, comment and all
D:P(A;CI;GR;;;BU)

[Registry.Sample] ; kept
HKR,Parameters,Kept,0x00010001,1
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if strings.Contains(got, "[Registry.Sample.Security]") || strings.Contains(got, "D:P(A;CI;") {
		t.Error("a commented descriptor section survived")
	}
	// The commented header after it must still be recognised, or everything below is lost.
	if !strings.Contains(got, "[Registry.Sample] ; kept") ||
		!strings.Contains(got, "HKR,Parameters,Kept,0x00010001,1") {
		t.Errorf("a section following a dropped one was swallowed:\n%s", got)
	}
	// The AddReg keeps the surviving section, drops the removed one — and keeps its comment,
	// which is a note about the directive and outlives the name that prompted it.
	if strings.Contains(got, "Registry.Sample.Security") {
		t.Errorf("AddReg still references a dropped section:\n%s", got)
	}
	if !strings.Contains(got, "AddReg = Registry.Sample ; two of them") {
		t.Errorf("a rewritten AddReg lost its trailing comment:\n%s", got)
	}
}

// A directive may be continued across physical lines. Deciding one physical line at a time
// would drop the head of a continued Security= and leave its tail as a stray directive, or
// strip a name from a continued AddReg and emit the continuation marker as if it were one.
func TestAdaptNetworkComponentHandlesLineContinuation(t *testing.T) {
	const src = `[Install.Sample]
AddReg = Registry.Sample.Parameters.Security, \
         Registry.Sample.Security, \
         Registry.Sample
Security = "D:(A;;CCLCSWLOCRRC;;;AU)" \
           "(A;;GA;;;BA)"

[Registry.Sample.Security]
D:P(A;CI;GR;;;BU)

[Registry.Sample.Parameters.Security]
D:P(A;CI;GA;;;BA)

[Registry.Sample]
HKR,Parameters,Kept,0x00010001,1
`
	got := string(AdaptNetworkComponent([]byte(src)))

	// The whole continued Security= goes, tail included.
	for _, s := range []string{"Security =", "D:(A;;CCLCSWLOCRRC;;;AU)", "(A;;GA;;;BA)"} {
		if strings.Contains(got, s) {
			t.Errorf("part of a continued Security= survived: %q\n%s", s, got)
		}
	}
	// The continued AddReg is rewritten as one line naming only what is left — no orphan
	// continuation marker, no reference to a section that is gone.
	if !strings.Contains(got, "AddReg = Registry.Sample\n") {
		t.Errorf("a continued AddReg was not reduced correctly:\n%s", got)
	}
	if strings.Contains(got, `\`) {
		t.Errorf("a continuation marker was left behind:\n%s", got)
	}
	if strings.Contains(got, "Registry.Sample.Security") {
		t.Errorf("a continued AddReg kept a dropped section:\n%s", got)
	}
	if !strings.Contains(got, "HKR,Parameters,Kept,0x00010001,1") {
		t.Errorf("the surviving section was lost:\n%s", got)
	}
}

// Two components in one file: the second declares ExcludeSetupStartServices but no CoServices
// of its own, and must not inherit the first's list — that would exclude the wrong services.
func TestAdaptNetworkComponentDoesNotLeakCoServicesAcrossSections(t *testing.T) {
	const src = `[Registry.First]
HKR,Ndi,CoServices,0x00010000,"Alpha","Beta"
HKR,Ndi,ExcludeSetupStartServices,0x00010000,"Alpha"

[Registry.Second]
HKR,Ndi,ExcludeSetupStartServices,0x00010000,"Gamma"
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if !strings.Contains(got, `HKR,Ndi,ExcludeSetupStartServices,0x00010000, "Alpha","Beta"`) {
		t.Errorf("the first component was not widened:\n%s", got)
	}
	if !strings.Contains(got, `HKR,Ndi,ExcludeSetupStartServices,0x00010000,"Gamma"`) {
		t.Errorf("the second component inherited the first's CoServices:\n%s", got)
	}
}

// A comment on the CoServices line describes that line. Carrying it into the value copied onto
// ExcludeSetupStartServices would attribute it to a line it was never written on.
func TestAdaptNetworkComponentDoesNotCarryTheCoServicesComment(t *testing.T) {
	const src = `[Registry.First]
HKR,Ndi,CoServices,0x00010000,"Alpha","Beta" ; the list
HKR,Ndi,ExcludeSetupStartServices,0x00010000,"Alpha" ; only this one
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if !strings.Contains(got, `HKR,Ndi,ExcludeSetupStartServices,0x00010000, "Alpha","Beta" ; only this one`) {
		t.Errorf("the exclude line did not keep its own comment and only its own:\n%s", got)
	}
	if strings.Count(got, "; the list") != 1 {
		t.Errorf("the CoServices comment was copied onto another line:\n%s", got)
	}
}

// [Strings] tokens may be named anything. A token named Security is a definition, not the
// directive this removes — dropping it leaves every %Security% reference unresolvable.
func TestAdaptNetworkComponentKeepsAStringsTokenNamedSecurity(t *testing.T) {
	const src = `[Install.Sample]
Security = D:(A;;CCLCSWLOCRRC;;;AU)

[Strings]
Security = "Security settings"
Other = "Something else"

[Strings.0419]
Security = "Параметры безопасности"
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if strings.Contains(got, "Security = D:") {
		t.Error("the Security= directive survived")
	}
	if !strings.Contains(got, `Security = "Security settings"`) ||
		!strings.Contains(got, `Security = "Параметры безопасности"`) {
		t.Errorf("a [Strings] token named Security was dropped:\n%s", got)
	}
}

// The reference-scrubbing has to cover every directive that names sections, not only AddReg:
// one left naming a dropped section fails the install exactly the same way.
func TestAdaptNetworkComponentScrubsOtherSectionLists(t *testing.T) {
	const src = `[Install.Sample]
DelReg = Registry.Sample.Security, Registry.Sample
BitReg = Registry.Sample.Security

[Registry.Sample.Security]
D:P(A;CI;GR;;;BU)
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if strings.Contains(got, "Registry.Sample.Security") {
		t.Errorf("a non-AddReg directive still names a dropped section:\n%s", got)
	}
	if !strings.Contains(got, "DelReg = Registry.Sample") {
		t.Errorf("DelReg lost the section that survived:\n%s", got)
	}
	if strings.Contains(got, "BitReg") {
		t.Error("a BitReg left with no sections was not dropped")
	}
}

// A name is not evidence. Needs and Include may point at a section in another INF, which this
// transform never saw and did not drop — removing that reference would break an install to fix
// nothing. Only a section actually removed from this file is scrubbed from the lists.
func TestAdaptNetworkComponentKeepsReferencesToSectionsItDidNotDrop(t *testing.T) {
	const src = `[Install.Sample]
Needs = Other.Install.Security
AddReg = Registry.Sample.Security, Registry.Sample

[Registry.Sample.Security]
D:P(A;CI;GR;;;BU)

[Registry.Sample]
HKR,Parameters,Kept,0x00010001,1
`
	got := string(AdaptNetworkComponent([]byte(src)))

	if !strings.Contains(got, "Needs = Other.Install.Security") {
		t.Errorf("a reference to a section in another file was scrubbed:\n%s", got)
	}
	// The one this file really did drop still goes.
	if strings.Contains(got, "AddReg = Registry.Sample.Security") {
		t.Errorf("a reference to a dropped section survived:\n%s", got)
	}
}

// A file with no security constructs and no Ndi block must come back byte-identical: the
// transform is only allowed to touch what it is for. A section list it has nothing to remove
// from counts as untouched, down to its spacing and its comments.
func TestAdaptNetworkComponentLeavesOrdinaryINFAlone(t *testing.T) {
	const plain = "[Version]\r\nSignature = \"$Windows NT$\"\r\n\r\n" +
		"[Install]\r\nAddReg=Registry.A,Registry.B ; keep this note\r\n" +
		"Needs = Other.Install\r\n\r\n[Files]\r\nfoo.sys = 1\r\n"

	if got := string(AdaptNetworkComponent([]byte(plain))); got != plain {
		t.Errorf("an INF with nothing to adapt was changed:\n got %q\nwant %q", got, plain)
	}
}

// CRLF endings survive, because setupapi reads the file we write and an INF on the media is
// CRLF throughout.
func TestAdaptNetworkComponentPreservesCRLF(t *testing.T) {
	src := strings.ReplaceAll(sampleINF, "\n", "\r\n")
	got := string(AdaptNetworkComponent([]byte(src)))

	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Error("a bare LF appeared in a CRLF file")
	}
	if !strings.Contains(got, `HKR,Ndi,ExcludeSetupStartServices,0x00010000, "Sample","Helper","Other"`+"\r\n") {
		t.Error("a rewritten line lost its CRLF")
	}
}

// The encoding a file arrives in is the encoding it leaves in: most INFs on XP media are
// UTF-16LE, and setupapi reads whatever it is handed.
func TestAdaptNetworkComponentRoundTripsUTF16(t *testing.T) {
	utf16le := encodeText(sampleINF, encUTF16LE)
	got := AdaptNetworkComponent(utf16le)

	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xFE {
		t.Fatalf("UTF-16LE input did not come back with a BOM: % x", got[:min(4, len(got))])
	}
	text, enc := decodeText(got)
	if enc != encUTF16LE {
		t.Errorf("encoding changed: got %v", enc)
	}
	if strings.Contains(text, "Security = D:") {
		t.Error("the transform did not apply through UTF-16")
	}
}
