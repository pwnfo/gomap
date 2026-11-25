# Streaming Compression Guide

Gomap streams generated chunks directly into pure-Go compressors without writing temporary uncompressed files to disk.

## Supported Formats

Use `-z` / `-compress` alongside `-o` / `-output`:

```bash
# Zstandard (recommended: ultra-fast speed + high ratio)
gomap '/l{5}' -workers 0 -compress zstd -output words.txt.zst

# Gzip (universal compatibility, fast)
gomap '/l{5}' -workers 0 -compress gzip -output words.txt.gz

# Bzip2 (high ratio for repetitive ASCII text)
gomap '/l{5}' -workers 0 -compress bzip2 -output words.txt.bz2

# LZMA / XZ (highest compression ratio, CPU-intensive)
gomap '/l{5}' -workers 0 -compress lzma -output words.txt.xz
```

If the output filename does not already end with the format's canonical extension (`.zst`, `.gz`, `.bz2`, `.xz`), Gomap appends it automatically. To protect existing archives from accidental data loss, compressed output refuses to overwrite an existing file unless `-fo` / `-force` is supplied.

## Format & Level Matrix

Control compression strength with `-zl` / `-compresslevel`:

| Format | Alias | Extension | Level Range | Default | Characteristics |
| :--- | :--- | :---: | :---: | :---: | :--- |
| `zstd` | `zst` | `.zst` | `1–9` | `3` | Best overall throughput-to-ratio trade-off |
| `gzip` | `gz` | `.gz` | `0–9` | `6` | Ubiquitous format, fast decompression |
| `bzip2` | `bz2` | `.bz2` | `1–9` | `9` | Strong Burrows-Wheeler compression on text |
| `lzma` | `xz` | `.xz` | `0–9` | `6` | Smallest archive size, slower encoding |

## Tuning `-flush-threshold` (`-ft`) and `-write-buffer` (`-b`)

- **`-ft, -flush-threshold`** (default `512KB`) controls the uncompressed chunk size dispatched by workers. Larger values (`1MB`–`4MB`) improve throughput and reduce synchronization overhead during multi-core runs.
- **`-b, -write-buffer`** (default `1MB`) controls the underlying file `bufio.Writer` size between the compressor and the OS kernel, minimizing `write(2)` syscalls.

```bash
gomap '/h{7}' -w 0 -z zstd -zl 3 -ft 2MB -b 4MB -n -o hex7.txt.zst
```
