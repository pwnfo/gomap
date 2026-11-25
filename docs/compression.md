# Streaming Compression Guide

Gomap streams generated chunks directly into pure-Go compressors without creating intermediate uncompressed files on disk.

## Supported Formats

Use `-z` / `-compress` together with `-o` / `-output`:

```bash
# Zstandard (multi-threaded encoder, high speed and compression ratio)
gomap '/l{5}' -w 0 -z zstd -o words.txt.zst

# Gzip (parallel pgzip block encoder when -w > 1 or -w 0)
gomap '/l{5}' -w 0 -z gzip -o words.txt.gz

# Bzip2 (strong compression on repetitive text)
gomap '/l{5}' -w 0 -z bzip2 -o words.txt.bz2

# LZMA / XZ (smallest output size, CPU-intensive)
gomap '/l{5}' -w 0 -z lzma -o words.txt.xz
```

If the output path does not end with the format's canonical extension (`.zst`, `.gz`, `.bz2`, `.xz`), Gomap appends it automatically. Existing compressed files are protected from accidental overwrite unless `-fo` / `-force` is passed.

## Format & Level Matrix

Control compression strength with `-zl` / `-compresslevel`:

| Format | Alias | Extension | Level Range | Default | Parallel Encoder |
| :--- | :--- | :---: | :---: | :---: | :--- |
| `zstd` | `zst` | `.zst` | `1-9` | `6` | Yes (`klauspost/compress/zstd`) |
| `gzip` | `gz` | `.gz` | `0-9` | `6` | Yes (`klauspost/pgzip` when workers > 1) |
| `bzip2` | `bz2` | `.bz2` | `1-9` | `9` | Single-stream |
| `lzma` | `xz` | `.xz` | `0-9` | `6` | Single-stream |

## Pre-Execution Size Estimation

Before starting a compressed run (or when inspecting a pattern with `-st -z <format>`), Gomap predicts the final compressed size:

* **Small wordlists (`<= 512 KB`)**: Compressed completely in memory to report the exact final byte count.
* **Large wordlists (`> 512 KB`)**: Uses $O(1)$ indexed seeking to sample four `128 KB` slices across the keyspace (`0%`, `33%`, `66%`, `95%`) through the selected codec and compression level, projecting the compressed size (`~X MB`) in milliseconds.

```bash
gomap '/l{4}' -st -z zstd -zl 6
```

## Progress Bar Behavior

* **Uncompressed output**: Displays processed bytes against total uncompressed bytes (`X MB / Y MB @ Z MB/s ETA ...`).
* **Compressed output (`-z`)**: Because compressed stream flushing happens in variable-ratio blocks, the progress bar switches to **word counts** with metric abbreviations (`K`, `M`, `B`, `T`) over the total number of entries:
  ```text
  ⠋ [ 50%] 3.66M / 7.31M words @ 18.40M/s ETA 00:01
  ```

## Tuning `-flush-threshold` (`-ft`) and `-write-buffer` (`-b`)

* `-ft, -flush-threshold` (default `512KB`) sets the uncompressed chunk size produced per worker ticket. Values between `1MB` and `4MB` reduce synchronization overhead on high core counts.
* `-b, -write-buffer` (default `1MB`) sets the underlying file `bufio.Writer` buffer size between the compressor and the OS.

```bash
gomap '/h{7}' -w 0 -z zstd -zl 6 -ft 2MB -b 4MB -n -o hex7.txt.zst
```
