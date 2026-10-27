# pe-builder documentation

- [`install.md`](install.md) — building on each platform (Linux, Windows, macOS)
  and the toolchain a desktop image needs.
- [`manifest.md`](manifest.md) — the build manifest: every block and field, what
  each one defaults to, and what the output targets and layouts produce.
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
- [`bartpe/`](bartpe/) — reference implementation documentation: a detailed,
  primary-source description of the original Bart's PE Builder, which pe-builder
  follows — its build process, the complete plugin `.inf` format, conventions, and
  internals.

The project's own policies:

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — how to get started, the conventions a
  change is expected to follow, and how AI-assisted contributions are handled.
- [`SECURITY.md`](SECURITY.md) — reporting a vulnerability privately, and what
  is in scope for a tool that assembles an image from media you supply.
- [`DISCLAIMER.md`](DISCLAIMER.md) — the full affiliation, trademark, and
  fitness-for-use notice the README summarizes.
