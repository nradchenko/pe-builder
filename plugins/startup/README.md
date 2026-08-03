<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# startup — the startup run-list contract

A registry contract for one-shot startup tasks, and the runner that walks it. This plugin
owns the contract and the runner only; the tasks themselves belong to whichever plugin
needs them, which declares `depends = startup` and adds its own entries.

## Why it exists

A `/minint` PE never runs the setup passes Windows would have run, so work that has to
happen exactly once at boot — a PnP device-install pass, a network component install — has
nowhere to live. `peshell` already reimplements several such passes in-process because
there was no alternative, but that answer does not generalize:

- **Layering.** Hosting the work in the shell would make networking require a desktop. A
  console recovery PE with a network is a legitimate build, and every component here is
  meant to be independently enablable.
- **Iteration.** A task hosted in the shell runs once per boot. A separate process can be
  re-run by hand, repeatedly, within one boot — which is exactly what bringing up hardware
  has needed all along.
- **Observability.** In a desktop build a console binary writes to a console nobody sees.
  "Why is there no network?" must have an answer short of attaching a serial debugger.

So the work runs out-of-process, and its progress travels back through a channel the shell
can paint.

## The contract

    HKLM\SOFTWARE\pe-builder\Startup\<task>
        Stage    = "pre-shell" | "post-shell"     REG_SZ
        Order    = dword                          ascending within a stage
        Label    = "Configuring devices..."       REG_SZ, shown to the user
        Command  = "pehw.exe devices"             REG_SZ, a command line
        Required = dword 0|1                      0 = best effort (default)
        Timeout  = dword seconds, optional        default 120

`<task>` is a short slug, and is also the name of the status key, so a task's declaration
and its verdict sit under one name.

Values are **explicit rather than path-encoded**. The Start-menu contract encodes its
structure into the path because it *is* a folder hierarchy; a run list is not, and explicit
values stay self-describing in a `hive dump` while letting a new field arrive later without
re-encoding every existing entry.

`Order` is authored in steps of `0x10` so an entry can be inserted between two others
without renumbering. Ties break on task name, so the resulting order is total and stable
rather than dependent on registry enumeration order.

A stage is an open string, not a declared enum: `pre-shell` and `post-shell` are the two the
engine sequences, and nothing validates the spelling. So a stage that matches no task is an
empty run rather than an error — `post-shell` legitimately has none until a plugin declares
one. `perun.exe` says *"no tasks are declared for stage X"* instead of a bare `0 ok` so a typo
is still visible. That is deliberately unlike `--task`, where a name matching nothing means the
specific thing you asked for is missing, and which fails.

`Stage` and `Command` are **not defaulted**. A missing one marks the entry malformed: it is
reported and recorded as skipped, never guessed. Defaulting a stage would run hardware
configuration at the wrong moment, and an entry with no command is a typo in some plugin's
`AddReg` — both must surface rather than vanish.

### Command

The command line to run, exactly as `CreateProcess` takes it, and **environment-expanded**
before it runs. So `%SystemRoot%\system32\pehw.exe devices` works, and is the form to prefer:
a PE's system drive letter is not fixed, and every other command-path contract in this project
is authored the same way — the Start-menu entries in `games.inf` and `accessories.inf`, which
`peshell` expands. A consumer following the house style therefore gets what it expects, rather
than `ERROR_FILE_NOT_FOUND` with the literal `%SystemRoot%…` recorded as the task's epitaph.

`REG_EXPAND_SZ` (`0x2` in a plugin `AddReg`) is the honest type for such a value, but the
expansion does not depend on it — a plain `REG_SZ` is expanded too. The failure that prevents
is worth more than enforcing the distinction.

`perun --list` prints the expanded command rather than the raw declaration, on the grounds
that the tool exists for debugging: what will actually run is the more useful answer.

### Status

Written back under the same key:

    Result      = dword   0 ok, 1 failed, 2 timed out, 3 skipped
    Error       = dword   the task's exit code — or a Win32 error, see below
    LastRun     = REG_SZ  local time
    Message     = REG_SZ  the last line the task printed

`Error` carries the task's exit code when the task actually ran. When it could not be started
at all — a missing binary, a pipe that could not be created — it carries the Win32 error
instead, because there is no exit code to report. The two are not distinguishable in a
`hive dump`: a task whose binary is absent records `Result=1, Error=2`, exactly like one that
ran and exited 2. `Message` usually separates them, since a task that never started printed
nothing.

The **runner** writes these, not the task — that is the point. A task that crashed, hung,
or was killed still gets a verdict, which is precisely the case you most need a record of.
The runner touches only these four names, so a task may keep its own counters (`Installed`,
`Failed`, …) under the same key without them being clobbered.

`Message` is captured from the task's own stdout, so a task can say something more specific
than an exit code without inventing an IPC channel.

`perun --status` reads all four back for every declared task, which is what makes `Message`
worth writing — at boot a task's output goes into the pipe the runner drains and reaches no
console, so its last line survives only here. A task with no `Result` yet reads as `never
run`, which distinguishes "declared and skipped" from "declared and never reached".

## The runner

`perun.dll` exports the walk, for a shell to load and drive with a progress callback so a task's
progress reaches whatever surface that shell already has, instead of a console nobody is looking
at. A task never runs inside the caller: the DLL only reads the registry and starts processes.

When a stage runs:

| Stage | Desktop build | Console build |
|---|---|---|
| `pre-shell` | before the shell is brought up | from `startnet.cmd` |
| `post-shell` | once the desktop is up | not run — "after the shell" means nothing without one |

Both front ends guard on the runner being staged rather than depending on it, so an image that
declares no tasks carries neither the runner nor a broken reference to one. Enabling this plugin
(directly, or through something that depends on it) is what puts the runner in the image.

    DWORD PerunRunStage(const wchar_t *stage, PERUN_PROGRESS cb, void *ctx, int *pFailedRequired);
    DWORD PerunRunTask (const wchar_t *task,  PERUN_PROGRESS cb, void *ctx, int *pFailedRequired);
    DWORD PerunEnumTasks(const wchar_t *stage, PERUN_ENUM cb, void *ctx);
    DWORD PerunEnumStatus(const wchar_t *stage, PERUN_STATUS cb, void *ctx);

`perun.exe` is the command line over the same code:

    perun --stage pre-shell     run one stage's tasks, in order
    perun --task net            re-run a single task by name
    perun --list [stage]        show the resolved list, run nothing
    perun --status [stage]      what each task recorded when it last ran

### Why perun.exe exists

Not for console builds. It exists because tasks are separate processes *so that they can be
re-run by hand*, and something has to re-run them — a payoff that is wanted most on a
**desktop** build, where the shell has already walked the list once. Open a command prompt,
`perun --list`, `perun --task net`, repeat, without rebooting. Console builds then get their
boot-time walk from the same binary for free.

`--list` is also the cheap way to check that a plugin's declaration actually reached the
hive, without booting anything to the point where it would run.

### What a task can rely on

Written from the task's side — these are the guarantees a task author can depend on, not how
they are implemented. (`perun.c` documents the mechanisms at the code that provides them.)

- **Its own process.** A task is a command line, started as a separate process. Nothing it does
  runs inside the caller, so a task may use setupapi, `INetCfg`, or anything else that would be
  reckless inside a shell.
- **Output is captured, always read.** stdout and stderr go to a pipe the runner drains
  continuously; a task can print as much as it likes without blocking. Its last non-blank line
  becomes `Message`.
- **stdin is `NUL`.** A task that tries to read input gets EOF immediately rather than blocking
  until its timeout — there is no console at boot, and nobody to answer a prompt. Do not write
  a task that expects input.
- **`Timeout` kills the whole tree.** A task that hangs is terminated along with anything it
  started, recorded as `Result=2`, and the walk moves on. Default 120 s; not theoretical, since
  `INetCfg::AcquireWriteLock` alone blocks for 15 s and a driver install can wedge outright.
- **Exiting normally leaves your children alone.** Only a timeout sweeps the process tree, so a
  task may deliberately leave something running — though see *Out of scope* on why that is a bad
  idea here.
- **A verdict is always written**, including when the task crashes or is killed, because the
  runner writes it rather than the task.

## A required task that fails

It is reported loudly — on the splash, in the status key, in `perun.exe`'s exit code — and
**the boot continues**. A desktop with no network beats no desktop. `PerunRunStage` itself
returns success whenever the walk ran; only a contract that could not be read at all is an
error. An absent contract key is an empty list, not a failure.

## The console path

`base`'s `startnet.cmd` carries a guarded call:

    if exist "%SystemRoot%\system32\perun.exe" "%SystemRoot%\system32\perun.exe" --stage pre-shell

Guarded rather than depended on, so `base` still boots as a bare prompt when this plugin is
absent — it names a file, not a plugin. The plugin format's `[Append]` section is not a
route to this: the engine parses it and deliberately does not apply it, and `startnet.cmd`
is rewritten verbatim from `base`'s own copy after the tree is assembled, so an append would
be overwritten even if it were applied.

There is no double-run risk. `explorer-shell` repoints `Setup\CmdLine` at `peshell.exe`,
replacing `cmd.exe /k startnet.cmd`, so on a desktop build `startnet.cmd` never runs at all —
the two front ends are mutually exclusive by construction rather than by convention.

## Proving the walk

The plugin ships a self-test task, `demo`, which echoes and exits 0. The `demo_task` option
sets the **stage it joins**, and defaults to `off` — a stage nothing ever runs. So by
default the task still appears in `perun --list` and in a `hive dump`, proving the contract
reached the hive, while costing a boot nothing:

    plugin "startup" {
      enabled = true
      options { demo_task = "pre-shell" }   # or "post-shell", or omit for "off"
    }

## Testing it on the host

There is no automated test for the runner yet. To exercise the list semantics — ordering and
tie-breaking, stage filtering, the malformed-entry path, `Timeout` and the tree kill, the
status values — run `perun.exe` under Wine against a real registry. It takes seconds, against
minutes for a guest boot:

    export WINEPREFIX=/tmp/perun-wine WINEARCH=win32
    wineboot -i
    K='HKLM\SOFTWARE\pe-builder\Startup\demo'
    wine reg add "$K" /v Stage   /t REG_SZ    /d pre-shell             /f
    wine reg add "$K" /v Order   /t REG_DWORD /d 16                    /f
    wine reg add "$K" /v Command /t REG_SZ    /d "cmd.exe /c echo hi"  /f
    wine perun.exe --list
    wine perun.exe --stage pre-shell
    wine reg query "$K"          # Result / Error / LastRun / Message, written by the runner

**`WINEARCH=win32` is not optional.** In a default 64-bit prefix a 32-bit `perun.exe` is
WOW64-redirected to `Wow6432Node` while `wine reg add` writes the 64-bit view, so the binary
reads an empty contract and reports `0 ok, 0 failed` — which looks exactly like a logic bug in
the walk. A win32 prefix has no redirection, and matches an i386 PE anyway.

This validates the runner's plumbing only. Wine is not a PE: no setupapi, no `INetCfg`, none of
the MiniNT quirks. What a task *does* still has to be verified in a guest.

## Out of scope

**Resident duties are not contract entries.** The visual style and the wallpaper must be
applied by a process that stays alive — a theme dies with the client that asked for it — so
they remain `peshell`'s own work. A run list that admitted "tasks that must not exit" would
be a plugin host, which this design rejects.

Pure registry configuration stays pure registry configuration. If a plugin can express
itself in an `AddReg`, it should; this contract is for work that needs code at run time.
