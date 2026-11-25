# Advanced Concepts

## Expression Script Files (`.gmap`)

Complex wordlist specifications can be maintained in `.gmap` script files and executed via `-f` / `-file`.

### Directives & Rules

- **Comments**: Full-line comments start with `# ` (hash + space/tab) or a standalone `#`. Inline comments start with ` # ` (space + hash + space). Note that `#[0-255]` at column 0 is parsed as a numeric range, not a comment.
- **`%define <name> <pattern>`**: Declares a reusable macro expanded wherever `$name;` appears on subsequent lines (including inside later `%define` declarations). Macro names cannot contain `$` or `;`.
- **`%include <path>`**: Binds an external wordlist file to the next expression line's `^` placeholder. Consecutive `%include` lines bind to consecutive `^` tokens in that next expression, after which the include queue resets automatically. Relative paths starting with `./` or `../` resolve relative to the script file's directory.

### Example (`payloads.gmap`)

```text
# Reusable macros
%define BASE (https://api.example.internal/)
%define VER  v#[1-3]
%define ID   #[0001-0500]

%include ./wordlists/users.txt
$BASE;$VER;/users/^?id=$ID; # Per-user endpoint permutations

$BASE;$VER;/health
```

Run with:

```bash
gomap -file payloads.gmap -workers 0 -output urls.txt
```

---

## Value Bindings (`<@name=expr>` & `<@name>`)

Standard concatenation computes the Cartesian product of independent tokens (`/d-/d` produces 100 entries: `0-0, 0-1, ..., 9-9`). Value bindings draw a value **once** and reuse that exact value throughout the entry:

* `<@name=expr>`: Evaluates `expr`, stores the current value in slot `name`, and emits it at this position.
* `<@name>`: Emits the value currently stored in `name`.

`name` must be a valid identifier (letter or `_`, followed by letters, digits, or `_`) and must be defined before it is referenced in the same expression.

```bash
$ gomap '<@d=/d>-<@d>' -q -n
0-0
1-1
...
9-9

$ gomap '<@u=^>:<@u>@corp.local' users.txt -q -n
admin:admin@corp.local
ryan:ryan@corp.local
```

Quantifiers work on both definitions and references:

```bash
# Repeat the stored value twice on the reference: 0_00, 1_11, ..., 9_99
$ gomap '<@d=/d>_<@d>{2}' -q -n
```

---

## Smart Mathematical Seeking (`-start` & `-end`)

Instead of generating and discarding entries from the beginning when resuming or sharding a job, Gomap computes the **exact global mixed-radix index** of `-start` and `-end` in milliseconds and initializes its odometer directly at that coordinate:

```bash
$ gomap '/a{6}' -start mmmmmm -end mmmmmz -q -n
mmmmmm
mmmmmn
...
mmmmmz
```

If either word cannot be produced by the pattern, Gomap immediately exits with a `[Pattern Error] Pattern does not produce word "..."` diagnostic.

---

## Deterministic Multi-Core Concurrency (`-workers`)

Pass `-w <N>` / `-workers <N>` (or `-w 0` to auto-detect CPU cores) to parallelize generation across goroutines:

```bash
gomap '/l{5}' -workers 0 -non-interactive -output five_letters.txt
```

### How Ordered Parallelism Works

1. The range `[StartIndex, StartIndex + TotalWords)` is partitioned into contiguous index tickets sized around `-flush-threshold`.
2. `N` worker goroutines pull tickets, initialize their local odometers directly via mixed-radix division, and fill reusable `[]byte` buffers from a `sync.Pool`.
3. A bounded sliding window (`3 * N` tickets) feeds a dedicated writer goroutine that drains contiguous sequence numbers in strict order, keeping output deterministic and memory usage bounded.
