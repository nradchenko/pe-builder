# games

The Windows XP games that run in a minimal PE, grouped under a **Games** folder in
*All Programs*: Solitaire, Minesweeper, FreeCell and Spider Solitaire.

## Card graphics

`cards.dll` holds the card faces and is deliberately **not** a seed:

- `sol.exe` and `freecell.exe` import it statically, so the closure already stages it.
- `spider.exe` does not reference it at all — it carries its own card art in its resources,
  which is why it is an order of magnitude larger than the other three.

Seeding it explicitly would imply the closure misses it, which it does not. If Solitaire or
FreeCell ever shows blank cards, that is the assumption to re-check first.

## What is left out, and why

- **Hearts** (`mshearts.exe`) — a network game; the PE has no network stack.
- **Pinball** (Space Cadet) — needs DirectX and sound, neither of which is present.

Both would mean bringing up the subsystem behind them first. A smaller working set beats an
entry that launches into an error.

## Notes

- All four resolve completely against an XP SP3 source: 13 modules each, nothing missing.
  Walking the *delay* imports too reaches `msjava.dll` — via
  `shell32.dll → shdocvw.dll → mshtml.dll` — but `[Closure]` follows only static imports,
  so that chain is never staged and never a red herring here.
- Minesweeper and Spider link `winmm.dll` for sound. There is no audio device in the PE yet;
  they run without one.