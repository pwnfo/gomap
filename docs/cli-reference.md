# CLI Reference & Flags

## Synopsis

```text
gomap [options] <pattern> [<files...>]
gomap [options] -file <script.gmap>
```

Gomap follows standard Go ecosystem single-dash flag conventions (`-flag` or `-flag=value`). Flags may appear **before, after, or interspersed** with positional arguments (`gomap '/d{4}' -workers 4 -output pins.txt` and `gomap -w 4 -o pins.txt '/d{4}'` are equivalent). Use `--` to stop flag parsing if a pattern itself begins with `-`.

---

## General Options

| Flag | Description |
| :--- | :--- |
| `-h, -help` | Print categorized usage help to `stdout` and exit `0`. |
| `-v, -version` | Print version, Go toolchain, and OS/architecture info and exit `0`. |
| `-st, -stats` | Compute and display pattern statistics (expressions, tokens, nodes, range filtering, exact word count, and byte size) without generating entries. |
| `-q, -quiet` | Suppress banners, timing summaries, confirmation prompts, and progress bars on `stderr`. Errors are still written to `stderr`. |
| `-n, -non-interactive` | Skip the interactive `› Press Enter to start` confirmation prompt. Automatically skipped when `stdin` or `stderr` is not a TTY. |

## Generation Options

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-d, -delimiter <str>` | `\n` | Delimiter appended after each generated entry (e.g., `,`, `:`, or shell `$'\t'` / `$'\0'`). |
| `-b, -write-buffer <size>` | `1MB` | Underlying output stream I/O buffer size (e.g., `64KB`, `4MB`, `32MB`). |
| `-w, -workers <0-256>` | `1` | Number of concurrent worker goroutines. Pass `0` to automatically use `runtime.NumCPU()`. Output remains strictly ordered regardless of worker count. |
| `-ft, -flush-threshold <size>` | `512KB` | Per-chunk byte threshold accumulated before flushing to the writer/compressor. |

## Input Options

| Flag | Description |
| :--- | :--- |
| `-f, -file <path>` | Read rules, `%define` macros, and `%include` directives from a `.gmap` script file. Pass `-` to read from `stdin`. Mutually exclusive with `<pattern>`. |
| `-s, -start <word>` | Seek mathematically to `<word>` and begin emitting from that entry (inclusive). |
| `-e, -end <word>` | Stop emitting immediately after producing `<word>` (inclusive). |

## Output Options

| Flag | Description |
| :--- | :--- |
| `-o, -output <path>` | Write generated entries to `<path>` instead of `stdout`. |
| `-fo, -force` | Overwrite the target compressed output file if it already exists. |
| `-z, -compress <fmt>` | Stream output through a compressor. Supported values: `gzip` (`.gz`), `zstd` (`.zst`), `bzip2` (`.bz2`), `lzma` / `xz` (`.xz`). Requires `-o` / `-output`. |
| `-zl, -compresslevel <n>` | Compression level for `-z`: `0–9` for `gzip` and `lzma`, `1–9` for `bzip2` and `zstd`. |

---

## Exit Codes

| Code | Meaning |
| :--- | :--- |
| `0` | Generation or `-stats` completed cleanly (including clean `EPIPE` when piped to `head`, etc.). |
| `1` | Invalid CLI arguments, pattern syntax error, unreachable `-start`/`-end` word, or file I/O error. |
| `130` | Generation interrupted by `SIGINT` (`Ctrl+C`) or `SIGTERM`. |
