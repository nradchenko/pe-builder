# compress test fixtures

Synthetic content generated for this test suite.

| File | What it is |
|---|---|
| `readme.txt`, `code.bin` | Plaintext inputs. `code.bin` is peppered with `0xE8` CALL patterns to exercise Intel E8 translation. |
| `hello.txt` | Small plaintext input. |
| `lzx-multi.cab` | LZX:21 cabinet holding `readme.txt` + `code.bin` in one folder (~64 KB uncompressed → spans multiple 32 KiB frames, so it exercises the per-frame bitstream realignment and E8 translation). |
| `lzx-single.cab` | LZX:21 single-file cabinet holding `hello.txt` (the loose `.??_` shape). |

The tests decode these cabinets and compare the result byte-for-byte against the
committed plaintext (independently verifiable with `cabextract`).

## Regenerating the `.cab` fixtures

The cabinets are produced by Microsoft's `makecab` driven by the real
`cabinet.dll` LZX compressor — the same encoder that built the Windows sources
this package targets — run under `wine` on Linux.

1. Obtain `makecab.exe` and `cabinet.dll` from a Windows source. On an XP/2003
   source they live in `I386` as `MAKECAB.EX_` (a single-file LZX cabinet — this
   package can decompress it) and `CABINET.DLL` (stored uncompressed).

2. `wine`'s built-in `cabinet.dll` only compresses with MSZIP, so the native
   `cabinet.dll` must be loaded instead. Place it **next to `makecab.exe`** and
   force the native DLL:

   ```sh
   cp CABINET.DLL "$(dirname makecab.exe)/Cabinet.dll"
   cat > lzx.ddf <<'EOF'
   .Set DiskDirectoryTemplate="."
   .Set CabinetNameTemplate="lzx-multi.cab"
   .Set Cabinet=ON
   .Set Compress=ON
   .Set CompressionType=LZX
   .Set CompressionMemory=21
   readme.txt
   code.bin
   EOF
   WINEDLLOVERRIDES="cabinet=n" wine makecab.exe /F lzx.ddf
   ```

   Notes: run with the working directory *inside* the wine drive (e.g. under
   `drive_c/`) — writing the cabinet to a path mapped via the `Z:` drive fails
   under wine. `CompressionMemory` is the window size in bits (21). Confirm the
   result really is LZX (not a silent store fallback) with
   `cabextract -i lzx-multi.cab`.

The plaintext inputs were produced by a small seeded generator; any content
works as long as it is your own.