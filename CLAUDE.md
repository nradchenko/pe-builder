# CLAUDE.md

Guidance for AI assistants and contributors working in this repository.

## Project

**pe-builder** is an open-source, cross-platform tool for building bootable Windows
PE environments from a user-supplied Windows installation source. It is a
clean-room, Linux-first reimplementation of the ideas behind the discontinued
*Bart's PE Builder*, aiming for compatibility with that tool's plugin format. See
[`README.md`](README.md) for the project overview.

## Repository layout

- `docs/` — project documentation.
  - `docs/reference/` — a primary-source reference describing the **original**
    Bart's PE Builder: its build process, the plugin `.inf` format, conventions,
    and internals. Treat these as the authoritative model of how the original tool
    behaves, and consult them before implementing anything format-related.

(Additional directories will be added as the implementation grows.)

## Ground rules

- **Clean-room.** This is an independent reimplementation. The original tool's
  source was never released; do not incorporate its binaries or any code from it.
- **No Microsoft files.** The tool operates on a Windows installation source that
  the *user* supplies. Never commit Windows/Microsoft files, nor any third-party
  installation media, to this repository.
- **Reference material stays local.** Any third-party binaries used to study the
  original tool are kept in a local, git-ignored cache and are never committed.

## Conventions

- Primary language: **Go**.
- Build definitions use **HCL**.
- Keep documentation and code descriptive of *what* things do; match the style and
  structure of the surrounding files.