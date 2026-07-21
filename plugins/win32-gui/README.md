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
resource id 123. Without an activation context both bind to v5, so their property sheets
draw a v5 tab control and v5 buttons around themed pages — visibly inconsistent under a
visual style. The `.manifest` files this plugin ships (attribute 1) are ours, and give each
host the context it never asks for.

An assembly reference names an architecture, so these manifests are architecture-specific
and their entries are scoped. On amd64 only `rundll32.exe` needs one, and it needs it far
more than x86 does — see below.

The assembly itself is sourced by exact path rather than by name, because `comctl32.dll`
and the manifest share basenames with other files a name lookup reaches first.

## Architecture

Both architectures stage the assembly, from different places and with different
consequences, so the `[SourcePaths]` entries are scoped per architecture.

On amd64 it is **not optional and not cosmetic**. Every shell binary the source ships
embeds a manifest requiring this assembly, and when it cannot be resolved the loader fails
the process outright — `explorer`, `notepad`, `taskmgr`, `calc` and the rest do not start
at all. There is no v5 fallback.

Three things differ from x86:

- **It is packed, not loose.** An amd64 source keeps the side-by-side assemblies inside
  `AMD64\ASMS01.CAB` rather than in a loose `ASMS` tree, so each entry names a member of
  that cabinet by its full internal path.
- **A publisher policy is required.** The amd64 assembly is version 6.0.3790.3959, while
  the binaries ask for 6.0.0.0 — they reach it only through the policy's
  `bindingRedirect`. x86 needs no policy at all: its media carry a real 6.0.0.0 assembly,
  so the bind is direct. That difference is why this whole problem is amd64-only.
- **The policy has to go where a PE looks for it** — see below.

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

Which hosts need a manifest, and how badly, differs between the two:

| | `rundll32.exe` | `explorer.exe` |
|---|---|---|
| x86 | no manifest | manifest at id 123 (ignored) |
| amd64 | **no manifest** | correct manifest at id 1 |

So on amd64 only `rundll32` is shipped one, from `rundll32.amd64.manifest`, staged under
the name `rundll32.exe.manifest`. Explorer needs nothing: its own manifest already asks for
the amd64 assembly, and an **embedded manifest takes precedence over an external file**
here (NT 5.2 — the opposite of the widely-repeated XP-era rule), so a file we shipped
beside it would simply be ignored.

The consequence of getting the architecture wrong is not cosmetic on amd64. Because
`rundll32` has no embedded manifest, our file is its *only* activation context — a
reference naming `x86` resolves to nothing, and every Control Panel applet fails to start
with `14001` rather than merely drawing v5 controls.

### Catalogs

The `.cat` files beside the manifest and the policy are staged for parity with a real
install's layout. Nothing measured shows the bind needs them — it failed identically with
and without them, before the `setuppolicies` cause was found — so they are not a
load-bearing part of the fix.
