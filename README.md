# pe-builder

An open-source, cross-platform tool for building bootable **Windows PE**
environments from a Windows installation source — a modern, Linux-first revival of
the ideas behind the classic, discontinued **Bart's PE Builder**.

> **Status: early development.** This project is a work in progress and does not
> yet produce bootable images. Formats, commands, and structure are subject to
> change, and there are no releases yet.

## What this is

The original *Bart's PE Builder* (freeware, Windows-only, now discontinued and
never open-sourced) assembled a bootable live Windows environment — "BartPE" —
from a genuine Windows XP / Server 2003 installation, driven by a simple but
powerful plugin system. Its plugin ecosystem, including the well-known desktop
shell plugin, turned a rescue disc into something close to a full Windows desktop.

**pe-builder** is a clean-room, open-source reimplementation of that idea, with two
goals:

- **Run anywhere** — build on Linux (and other platforms), not just Windows.
- **Stay compatible** — understand the original plugin format so the existing
  body of community plugins can be reused.

## Bring your own Windows

Like the original, pe-builder ships **no Microsoft files**. You supply your own
licensed Windows installation source (Windows XP Service Pack 1 or later, or
Windows Server 2003). The tool only reads and repackages files from the source you
provide.

## Documentation

- [`docs/reference/`](docs/reference/) — a detailed, primary-source reference for
  the original Bart's PE Builder: its build process, the complete plugin `.inf`
  format, conventions, and internals. This is the foundation the implementation is
  built on.

## License

Copyright © 2026 Nikita Radchenko

Licensed under the [Apache License 2.0](LICENSE).

## Disclaimer

This project is **not affiliated with, endorsed by, or derived from the code of**
Bart Lagerweij or the original PE Builder (no source for which was ever released).
Windows, Windows XP, and Windows Server are trademarks of Microsoft Corporation.
"BartPE" refers to the environment produced by the original tool and is used here
only for description and compatibility.