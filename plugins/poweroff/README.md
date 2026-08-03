<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# poweroff

Powers the machine off from the command shell. A `/minint` PE has no service that can do it, so
this adds a small `poweroff.exe` to `system32` (on the `PATH`).

Enable it from the manifest like any other plugin:

```hcl
plugin "poweroff" {
  enabled = true
}
```

(The `.inf` declares `Enable = 1`, but that field is descriptive metadata — what a build contains is
decided by the manifest alone, so a plugin no manifest names is not built.)

## Why a tool is needed at all

The ordinary ways to power a Windows machine off all route through something a PE does not run:

- **`ExitWindowsEx`** asks the Winlogon shutdown coordinator over RPC. There is no Winlogon, and
  without RPCSS the call fails with "device not ready".
- **`shutdown.exe`** is the same path behind a command line, and is not staged anyway.
- **The ACPI power button** is handled by the power policy manager acting on a user session. With no
  session it is inert — pressing it in the VM does nothing.

What *is* available is the kernel's own shutdown entry point, `NtShutdownSystem`, which drives the
HAL straight into ACPI S5. It needs no coordinator, no session and no service — only the calling
token's `SeShutdownPrivilege`. That is the whole program.

The same reasoning produced `peshut` in `explorer-shell`, which makes the *shell's* "Turn Off
Computer" work by patching `ExitWindowsEx` to reach the same call. Both end up at
`NtShutdownSystem(ShutdownPowerOff)`, so the kernel flushes filesystems and disks identically —
this is not a "hard" power-off that risks the output volume.

## Enabling the privilege without LSA

`AdjustTokenPrivileges` needs the privilege's LUID, and the obvious way to get one is
`LookupPrivilegeValue` — which asks the **LSA policy database**. In a minimal PE that database may be
uninitialized, and the lookup fails.

`SeShutdownPrivilege` has a **fixed well-known LUID of `{19, 0}`**, so the program writes it directly
and never asks LSA. `AdjustTokenPrivileges` itself acts on the process token, a kernel object, so it
works regardless of LSA's health.

Note the check afterwards: `AdjustTokenPrivileges` returns success even when it adjusted *nothing*,
so the result is read from `GetLastError()`, not the return value.

## Exit codes

On success the machine powers off and the program never returns. If it returns, the code says why:

| Code | Meaning |
|---|---|
| `1` | `OpenProcessToken` failed. |
| `3` | `SeShutdownPrivilege` is not held by the token — the adjust did not take. |
| `4` | `NtShutdownSystem` was not found in `ntdll.dll`. |
| *negative* | The `NTSTATUS` from `NtShutdownSystem` — the kernel refused. That is a HAL/ACPI question, not a wiring problem. |

## Build

`poweroff.exe` is cross-compiled with **MinGW-w64** — no MSVC, no SDK. The source is
architecture-neutral and this is a plain user-mode binary, so the architecture selects the toolchain
and nothing else; `make ARCH=i386` (the default) and `make ARCH=amd64` pick it, and the plugin's
`[Build.<arch>]` sections name each architecture's compiler.

By hand, for a 32-bit target:

```sh
make ARCH=i386
```

The link is reproducible — `-Wl,--no-insert-timestamp` zeroes the PE `TimeDateStamp`, so the same
source builds byte-identical output. The `VERSIONINFO` resource is compiled from the engine-supplied
template (`pebuild/version.rc`, with the in-tree `../.shared/version.rc` as the by-hand fallback);
its values arrive in the environment from `internal/buildinfo`.
