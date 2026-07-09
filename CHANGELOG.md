# Changelog

All notable changes to pe-builder are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-07-09

First working release. Builds a bootable Windows PE that boots to an interactive shell,
from a user-supplied Windows XP source, entirely on Linux — no Windows host required. A
build runs in about 4 seconds and produces a roughly 45 MB ISO.

### Added

- **Source reading** — read a Windows installation source (directory or ISO) and probe
  its identity (build number, architecture, flavor).
- **Cabinet decompression** — a pure-Go MSCF (CAB-LZX) decoder for the source's
  compressed files.
- **INF engine** — a Windows setupapi-compatible `.inf` parser that compiles to a build
  action IR (copy-file, make-directory, registry, text-edit).
- **Layout resolution** — resolve the source's `txtsetup.sif` / `layout.inf` (file →
  target directory, on-disc name, boot-driver lists), and derive its locale from the
  `[nls]` section.
- **Clean-room base layer** — boots a source to an interactive `cmd.exe`: a minimal file
  set with the boot, service, and registry configuration, keyboard, mouse, and VGA, plus
  the source's own locale (code pages, console font, keyboard layout).
- **Registry hives** — build the Windows registry hives with a pure-Go `regf`
  reader/writer.
- **File tree and bootable ISO** — assemble the output file tree and master a bootable
  ISO9660 image in pure Go, with no external `mkisofs`/`xorriso` dependency.
- **CLI** (`pebuild`) — `build`, `validate`, `source probe`, and `version`.

### Known limitations

- The image cannot power itself off in the minimal-install environment; power the
  virtual machine off externally.
- Targets Windows XP sources; other source flavors and output targets are planned.
- No plugin system yet — a build is the base layer only.

[0.1.0]: https://github.com/nradchenko/pe-builder/releases/tag/v0.1.0
