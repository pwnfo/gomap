# Expressions, Classes & Quantifiers

At the core of Gomap is a fast, deterministic pattern compiler. Every position in an expression represents a set of choices combined left-to-right in Cartesian order (unless linked via [Value Bindings](advanced.md#value-bindings)).

## Literal Characters

Plain characters emit themselves verbatim:

```bash
$ gomap 'admin/d' -q -n
admin0
admin1
...
admin9
```

## Built-in Character Classes

Shortcuts prefixed with `/` expand into predefined character classes:

| Token | Meaning | Characters |
| :--- | :--- | :--- |
| `/l` | Letters (mixed case) | `a–z` + `A–Z` (52 chars) |
| `/a` | Lowercase letters | `abcdefghijklmnopqrstuvwxyz` |
| `/A` | Uppercase letters | `ABCDEFGHIJKLMNOPQRSTUVWXYZ` |
| `/d` | Digits | `0123456789` |
| `/D` | Non-zero digits | `123456789` |
| `/h` | Hexadecimal (lower) | `0123456789abcdef` |
| `/H` | Hexadecimal (upper) | `0123456789ABCDEF` |
| `/o` | Octal digits | `01234567` |
| `/p` | Special characters | `!@#$%^&*-_+=` |
| `/s` | Single space | `' '` |
| `/b` | Newline | `\n` |

Unknown `/` sequences (such as `/z`) are preserved literally.

## Custom Classes & Word Unions (`[...]`)

Wrap characters in square brackets to pick one character per position:

```bash
$ gomap '[abc][12]' -q -n
a1
a2
b1
b2
c1
c2
```

### Mixing Built-in Shortcuts Inside Brackets

Built-in `/` tokens expand inline inside `[...]`:

```bash
# Lowercase alphanumeric + underscore + hyphen (38 choices)
$ gomap '[/a/d_-]{4}' -st
```

### Word Unions (`|`)

When `[...]` contains an unescaped pipe `|`, each segment becomes a multi-character word alternative:

```bash
$ gomap '[admin|root|operator]_[prod|dev]' -q -n
admin_prod
admin_dev
root_prod
root_dev
operator_prod
operator_dev
```

## Literal Groups (`(...)`)

Parentheses wrap a literal string as a single indivisible unit without expanding `/` shortcuts inside it. This is particularly handy when attaching quantifiers to words or URL prefixes:

```bash
$ gomap '(https://example.com/v1/)[users|orders]' -q -n
https://example.com/v1/users
https://example.com/v1/orders

$ gomap '(ha){3}' -q -n
hahaha
```

## Quantifiers

Quantifiers modify the immediately preceding token (a single literal rune, `[...]`, `(...)`, `#[...]`, `^`, or `<@...>`):

* `{N}`: Exact repetition (`N >= 0`).
* `{min,max}`: Inclusive repetition range (`0 <= min <= max`), ordered from `min` length up to `max` length.
* `?`: Shorthand for `{0,1}` (optional).

```bash
$ gomap '[01]{1,2}' -q -n
0
1
00
01
10
11

$ gomap 'Ryan?/d' -q -n
# '?' applies to 'n', yielding Rya0..Rya9 followed by Ryan0..Ryan9
```

## Top-Level Expression Alternation (`||`)

Chain completely distinct expressions in a single run using `||`:

```bash
$ gomap 'admin#[01-05]||guest#[100-105]' -q -n
```

## Escaping (`\`)

Prefix any special character (`/`, `[`, `]`, `(`, `)`, `{`, `}`, `?`, `^`, `<`, `#`, `|`, `\`) with `\` to emit it literally:

```bash
$ gomap '\/d=/d' -q -n
/d=0
/d=1
...
```
