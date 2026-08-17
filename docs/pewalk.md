# Inspecting dependencies with `pewalk`

`pewalk` is a standalone companion tool — an "ldd for PE". Given a Windows binary and a
source (a directory or `.iso`), it computes the binary's transitive **import
closure** — every module it depends on, directly or indirectly, through both static and
delay-load imports — and reports where each one lives in the source. It resolves modules
in every form a Windows source stores them: loose files, compressed `.??_` files, and
files packed inside cabinets such as `DRIVER.CAB`. A module that the source does not
provide is flagged as **missing** rather than treated as an error, which makes it easy to
see what a binary would need but the media lacks.

```sh
go build -o pewalk ./cmd/pewalk

# the import closure of Explorer, resolved inside an ISO
./pewalk --iso win.iso explorer.exe

# just the modules that are missing from the source
./pewalk --iso win.iso --missing-only ntoskrnl.exe win32k.sys

# only the static imports — the modules that must be present for the seed to load
./pewalk --iso win.iso --static-only ntoskrnl.exe
```

Delay-load imports resolve lazily at runtime and often never load in a minimal
environment, so `--static-only` narrows the closure to the static import graph — the
boot-necessary set — dropping the delay-only branches the default walk follows.

Output is a dependency tree by default, or `--format dot` (Graphviz) / `--format json`.
The closure is reported at DLL granularity — which modules a binary imports, not which
functions.

A full closure of a GUI binary is large and densely connected (a shell like
`explorer.exe` reaches ~125 modules through `shell32`, and `kernel32` alone is imported by
almost all of them), so a naive `--format dot | dot` produces an enormous, unreadable
canvas. To get a legible graph, either bound the depth or run the DOT through Graphviz's
transitive-reduction filter `tred` first:

```sh
# bound the recursion for a compact picture
./pewalk --iso win.iso --max-depth 2 explorer.exe --format dot | dot -Tsvg -o deps.svg

# or reduce the full graph to its essential edges before laying it out
./pewalk --iso win.iso explorer.exe --format dot | tred | dot -Tsvg -o deps.svg
```

The text and JSON formats scale to the full closure without this concern.
