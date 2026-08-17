# pe-builder documentation

- [`install.md`](install.md) — building on each platform (Linux, Windows, macOS)
  and the toolchain a desktop image needs.
- [`supported.md`](supported.md) — which Windows sources are supported and what
  each one produces, the output targets, and the hardware an image expects.
- [`inspecting.md`](inspecting.md) — inspecting a source or a build:
  extracting files, listing what the media carries, reading INF sections and
  registry hives.
- [`pewalk.md`](pewalk.md) — the `pewalk` dependency inspector: computing a
  Windows binary's transitive import closure against a source.
- [`wim-boot.md`](wim-boot.md) — the `wim` layout: booting a compact,
  open-source Windows PE from a WIM, how the chain works, and a build walkthrough.
- [`plugins/`](plugins/) — using and writing plugins: a short guide, and
  the policy for what may be bundled with pe-builder.
- [`reference/`](reference/) — a detailed, primary-source reference for
  the original Bart's PE Builder: its build process, the complete plugin `.inf`
  format, conventions, and internals. This is the foundation the implementation is
  built on.
