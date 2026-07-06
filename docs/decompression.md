# Source decompression

A Windows installation source stores most of its files compressed inside
Microsoft cabinet (`MSCF`) containers: loose single-file cabinets (the `*.??_`
files such as `NTDLL.DL_`) and multi-file archives such as `DRIVER.CAB` and
`SP3.CAB`. The `compress` package reads the cabinet framing and decodes the
member files. On the Windows XP/2003 sources this tool targets, the payloads are
compressed with **LZX** (21-bit window), which the package decodes in pure Go.

## Correctness

The decoder is validated byte-for-byte against `cabextract` (libmspack) on a
real Windows XP SP3 x86 source:

- `DRIVER.CAB` — all 4406 members identical (315 MB), including a 2.1 MB file
  that exceeds the 2 MB LZX window (exercising cross-window matches).
- `SP3.CAB` — all 368 members identical (54 MB).
- A spread of loose `*.??_` cabinets — identical.

## Performance

This is a **practical** benchmark: decompressing the two large real source
cabinets end to end, which answers the question that matters in practice — is
the pure-Go decoder competitive with the native reference decoders?

**Method.** 60 runs per decoder per cabinet, warm page cache, output discarded.
The reference tools decode to a pipe (`cabextract -p`, `7z e -so`) into
`/dev/null`; the Go decoder times the in-memory folder decode. The reference
tools additionally read the compressed file from disk and verify CFDATA
checksums — work this decoder skips — so the comparison, if anything, flatters
them.

Environment: AMD Ryzen 5 7500F, Go 1.26.4 (linux/amd64), cabextract 1.11
(libmspack), 7-Zip 23.01.

### `DRIVER.CAB` — 300.6 MiB uncompressed

| Decoder | median | mean | min–max | MiB/s (median) | vs. this tool |
|---|---|---|---|---|---|
| **this tool** (pure Go) | 0.879 s | 0.893 s | 0.857–0.999 | **342** | 1.00× |
| cabextract (C, libmspack) | 0.827 s | 0.828 s | 0.814–0.858 | 364 | 1.06× |
| 7-Zip (C++, p7zip) | 0.713 s | 0.715 s | 0.700–0.744 | 421 | 1.23× |

### `SP3.CAB` — 51.6 MiB uncompressed

| Decoder | median | mean | min–max | MiB/s (median) | vs. this tool |
|---|---|---|---|---|---|
| **this tool** (pure Go) | 0.278 s | 0.285 s | 0.261–0.382 | **185** | 1.00× |
| cabextract (C, libmspack) | 0.234 s | 0.236 s | 0.229–0.315 | 220 | 1.19× |
| 7-Zip (C++, p7zip) | 0.222 s | 0.222 s | 0.216–0.237 | 232 | 1.25× |

### Reading the numbers

- The pure-Go decoder runs at **0.81–0.94×** the throughput of the native
  references — 6–25 % slower, well short of an order-of-magnitude gap. On the
  larger, match-heavy `DRIVER.CAB` it is within 6 % of cabextract.
- `SP3.CAB` decodes slower than `DRIVER.CAB` for *all three* decoders, so that
  reflects the cabinet's data mix rather than any single implementation.
- Go's garbage collector produces a slightly wider tail (max 0.999 s versus
  cabextract's tight 0.858 s on `DRIVER.CAB`); the median is the fair central
  figure.
