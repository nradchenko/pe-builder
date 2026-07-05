# Bart's PE Builder — Reference Documentation

A clean-room reference description of **Bart's PE Builder** (the original,
discontinued freeware tool) and the plugin ecosystem built around it — most
notably Sherpya's Windows XPE plugin.

Its purpose is to record everything known about how the original project works,
derived from primary sources. These documents describe the original tool only.

## Documents

| File | Topic |
|------|-------|
| [`01-overview.md`](01-overview.md) | What PE Builder / BartPE is, versions, licensing, requirements, what it produces |
| [`02-build-process.md`](02-build-process.md) | The build engine: inputs, phases, registry hives, boot configuration & chain, ISO/USB, drivers, slipstream, GUI & CLI |
| [`03-plugin-format.md`](03-plugin-format.md) | The plugin `.inf` file format — every section, grammar, directory IDs, attributes, registry mapping, conditional builds |
| [`04-plugin-conventions.md`](04-plugin-conventions.md) | Plugin folder layout, the plugin dialog, `.cab` packaging, load ordering, `Config=` configurators, vendor `[Update]` extension |
| [`05-case-study-xpe.md`](05-case-study-xpe.md) | Sherpya's Windows XPE plugin — bundle composition, dependencies, boot/init sequence, customization & boot-fix patterns |
| [`06-bundled-plugins.md`](06-bundled-plugins.md) | Catalogue of the plugins shipped inside PE Builder 3.1.10a |
| [`07-engine-internals.md`](07-engine-internals.md) | Findings from static analysis of the `pebuilder.exe` / `bartpe.exe` binaries — toolchain, the setupapi INF engine, hive-building method, exact ISO command, source version gate |

## Primary sources

Everything here is derived from, and cross-checked against, actual artifacts:

- **PE Builder 3.1.10a** (`pebuilder3110a.exe`, an Inno Setup 5.1.2 installer,
  released 2006-02-17), in particular:
  - `pebuilder.inf` — the base build manifest (the "base layer").
  - `help/english/*.htm` — the authoritative help, including `pluginformat.htm`
    (the plugin format specification), `plugin.htm` (the plugin dialog),
    `options.htm`, `cmdlineoptions.htm`, `drivers.htm`, `slipstream.htm`,
    `main.htm`.
  - the ~32 bundled plugins.
  - `mkisofs.exe`, `cdrecord.exe`, `StarBurn.dll`, `pe2usb.cmd` / `pe2usb.bin` —
    the ISO/burn/USB toolchain.
- **Sherpya Windows XPE 1.0.7** (`xpe-1.0.7.cab`) — the XPE plugin bundle.
- The archived nu2.nu website (homepage, plugins list, FAQ).

## A note on licensing / provenance

PE Builder's own binaries (`pebuilder.exe`, `bartpe.exe`, `netconfig.exe`,
`scangui.exe`) ship under a restrictive freeware license (see
[`01-overview.md`](01-overview.md)): free to use and redistribute *unmodified and
in the original archive*, but may not be sold, modified, or incorporated into
other software, and no source was ever released. Its bundled `mkisofs` /
`cdrecord` are GPL (Jörg Schilling); the burn library is the proprietary StarBurn
SDK. PE Builder never distributed any Microsoft Windows files — the user always
supplies their own licensed Windows source media.

This documentation records facts and file-format interfaces observed from the
tool's own data files and behavior. It does not reproduce PE Builder's code.