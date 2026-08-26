<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# win32-gui

The shared Win32 GUI runtime: the libraries a graphical program needs beyond the base
layer's console set, so plugins that ship GUI applications depend on this one rather than
each listing the same DLLs.

"Win32" names the API, not the word size — the plugin is architecture-neutral and stages
whatever the source carries. On an amd64 source that is the 64-bit runtime.

## What it stages

The file set is computed, not listed: `[Closure]` names six seeds — `shell32`, `comctl32`,
`shlwapi`, `comdlg32`, `ole32`, `oleaut32` — and the engine walks their static import
closure against the source, so the runtime's transitive dependencies come from the
source's own dependency graph rather than a hand-maintained list that would drift.

## The Common-Controls 6.0 assembly, and why the shells need a manifest

XP ships two versions of the common controls. Version 5 is `system32\comctl32.dll`;
version 6 — the themed one — is a side-by-side assembly under `WinSxS`, and a process
gets it only by *asking*, through an application manifest naming version 6.0.0.0.

Staging the assembly is therefore not enough. The loader honours an application manifest
only at resource id 1, and on x86 neither shell host qualifies: `rundll32.exe`, which hosts
every Control Panel applet, carries no manifest at all, and `explorer.exe` carries one at
resource id 123. Without an activation context both bind to v5, and what that costs depends
on the build: on 2600 their property sheets open and draw a v5 tab control and v5 buttons
around themed pages — visibly inconsistent under a visual style — while on 3790 they do not
open at all. The `.manifest` files this plugin ships (attribute 1) are ours, and give each
host the context it never asks for.

An assembly reference names an architecture, so these manifests are architecture-specific
and their entries are scoped. Both hosts need one on both architectures; what differs is the
price of getting it wrong, which on amd64 is a process that does not start — see below.

The assembly itself is sourced by exact path rather than by name, because `comctl32.dll`
and the manifest share basenames with other files a name lookup reaches first.

## Where the assembly comes from

Every source stages the assembly, but from different places and with different
consequences, so the `[SourcePaths]` entries are scoped.

**The split follows the product, not the architecture.** Windows XP x86 is the odd one out;
Server 2003 — on either architecture — ships the assembly the way an amd64 source does,
because XP Professional x64 Edition and Server 2003 are one codebase. So the x86 entries are
scoped by build number rather than by `i386` alone.

|  | XP x86 (2600) | Server 2003 and amd64 (3790) |
|---|---|---|
| Stored | loose under `ASMS\6000` | packed in `ASMS01.CAB` |
| Version | 6.0.0.0 | 6.0.3790.3959 |
| Publisher policy | not needed | **required** |

Where the assembly is packed, each entry names a cabinet member by its full internal path.
Where it is versioned 6.0.3790.3959, binaries asking for 6.0.0.0 reach it only through the
policy's `bindingRedirect`; XP x86 media carry a real 6.0.0.0 assembly, so that bind is
direct and needs no policy. The policy also has to go where a PE looks for it — see below.

On amd64 this is **not optional and not cosmetic**: every shell binary embeds a manifest
requiring the assembly, and when it cannot be resolved the loader fails the process outright
— `explorer`, `notepad`, `taskmgr`, `calc` and the rest do not start at all. There is no v5
fallback.

There is deliberately no unsuffixed x86 fallback. The two layouts share no path, so either
one used as a default would be wrong for the other, and a build the probe cannot classify has
nothing to choose with — such a source stages no assembly and its shell binaries do not
start. Supporting another build means adding its section.

### Where the policy has to live

A PE runs with `Setup\SetupType=1`, and **in setup mode the loader reads publisher
policies from `WinSxS\setuppolicies`, not `WinSxS\Policies`.** A real install carries both
trees; staging only `Policies` leaves the policy silently unconsulted.

The failure that causes is worth recognising, because nothing about it points at the
policy: the assembly still resolves when a binary asks for it *by its own version*, every
file is present and byte-identical to a real install, every directory enumerates, and the
names are correct — yet every request that needs the 6.0.0.0 redirect fails with
`14001 ERROR_SXS_CANT_GEN_ACTCTX`. The policy is ignored outright rather than misapplied,
so the symptom is indistinguishable from a missing assembly.

So the policy is staged twice, into both trees. The `setuppolicies` copy is the one that
makes the bind work. A real install ships no catalog beside that copy, so neither do we.

### The external manifests, per architecture

The loader takes a process's manifest from resource **id 1** and, finding one, does not look
beside the binary: an **embedded manifest takes precedence over an external file** here (NT 5.2 —
the opposite of the widely-repeated XP-era rule). What each host carries at id 1 is therefore the
whole question:

| | `rundll32.exe` | `explorer.exe` |
|---|---|---|
| 2600 x86 | no manifest | id 123 only — **no id 1** |
| 3790 x86 | no manifest | id 123, plus **one byte at id 1** |
| 3790 amd64 | no manifest | id 123, plus **one byte at id 1** |

Explorer's real manifest is at id 123 on every source, which the loader does not read. On 2600
that leaves id 1 empty, so the file beside the binary is used. On 3790 a **one-byte** resource
sits at id 1: enough to count as an embedded manifest and win, carrying no assembly reference to
give. Explorer then runs with a v5 default context, and `InitCommonControlsEx` in v5 refuses the
standard classes a property sheet is built from — so Folder Options, a drive's Properties and a
connection's Status open nothing at all, rather than opening with v5 controls.

`PreferExternalManifest` reverses that precedence, and the plugin sets it for 3790 only (2600's
loader does not implement the value and does not need it). With it in force Explorer loads the v6
comctl32 and no longer maps the v5 one at all, and all three sheets open on both architectures.

Getting the architecture wrong is not cosmetic. Our file is `rundll32`'s *only* activation
context, and with `PreferExternalManifest` in force it is effectively Explorer's too — a
reference naming `x86` on an amd64 build resolves to nothing, and a process whose manifest
resolves to nothing does not start. For `rundll32` that costs every Control Panel applet
(`14001`); for Explorer it would cost the shell. Hence `explorer.amd64.manifest` beside
`rundll32.amd64.manifest`, each staged under the name the loader looks for.

### Catalogs

The `.cat` files beside the manifest and the policy are staged for parity with a real
install's layout. Nothing measured shows the bind needs them — it failed identically with
and without them, before the `setuppolicies` cause was found — so they are not a
load-bearing part of the fix.
