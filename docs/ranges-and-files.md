# Numeric Ranges & File Placeholders

## Numeric Ranges (`#[start-end:step]`)

Generate ascending or descending arithmetic sequences anywhere in a pattern using `#[start-end]` or `#[start-end:step]`:

- **Ascending**: `#[1-5]` → `1, 2, 3, 4, 5`
- **Descending**: `#[5-1]` → `5, 4, 3, 2, 1`
- **Custom Step**: `#[0-10:2]` → `0, 2, 4, 6, 8, 10`
- **Negative Step**: `#[10-0:-3]` → `10, 7, 4, 1`

### Automatic Zero-Padding

When either bound has a leading zero on a multi-digit number (such as `01` or `000`), Gomap automatically zero-pads every value in the range to the maximum bound width:

```bash
$ gomap '2026-#[01-12]-#[01-31]' -st
# Produces 2026-01-01, 2026-01-02, ..., 2026-12-31

$ gomap 'pin_#[0000-9999]' -st
# Produces pin_0000 .. pin_9999
```

> Single-digit `0` (such as `#[0-9]` or `#[0-255]`) does **not** trigger zero-padding, maintaining full compatibility with IP octets and unpadded counters.

### Unmaterialized Large Ranges

Ranges up to `65,536` values are pre-formatted in memory for speed. Ranges larger than `65,536` values (e.g., `#[0-4294967295]`) are evaluated arithmetically on the fly, using constant memory and computing `-stats` and `-start` seeks in `O(1)` time.

---

## External File Placeholders (`^`)

Each unescaped `^` token binds to the next positional file path passed on the command line and iterates over its lines (`\r` and `\n` stripped):

```bash
$ gomap '^:^' examples/wordlists/users.txt examples/wordlists/pass.txt -q -n
admin:123456
admin:12345
...
```

Quantifiers work directly on `^` placeholders:

```bash
# All 2-word combinations from words.txt
$ gomap '^{2}' words.txt -q -n
```

---

## Inline Macros (`//`)

To substitute a sub-expression into a `^` placeholder from the CLI without creating a temporary file, prefix the argument with `//`:

```bash
$ gomap 'svc-^-^' '//[auth|billing]' users.txt -q -n
```

Positional alignment is preserved regardless of the order in which real files and `//` inline arguments are mixed:

```bash
$ gomap '^-^' users.txt '//#[01-03]' -q -n
# Emits <user>-01, <user>-02, <user>-03 for every line in users.txt
```
