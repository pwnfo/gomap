package gomap

import "strings"

// DefaultWildcard is the default prefix character used for built-in character classes.
const DefaultWildcard = "/"

// CharClasses maps built-in shortcut characters to their expanded character sets.
var CharClasses = map[byte]string{
	'd': "0123456789",
	'D': "123456789",
	'h': "0123456789abcdef",
	'H': "0123456789ABCDEF",
	'a': "abcdefghijklmnopqrstuvwxyz",
	'A': "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	's': " ",
	'o': "01234567",
	'p': "!@#$%^&*-_+=",
	'l': "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
	'b': "\n",
}

// ExpandShortcuts expands built-in character class shortcuts (such as /d, /l, /a, /A)
// using the specified wildcard prefix (defaults to "/" if empty).
//
// Shortcuts outside brackets expand to "[...]".
// Shortcuts inside "[...]" expand inline (unless separated by unescaped '|' alternatives,
// where standalone shortcuts like [/d|admin] also expand cleanly).
// Shortcuts inside "(...)" literal groups are left untouched.
func ExpandShortcuts(pattern string, wc string) string {
	if wc == "" {
		wc = DefaultWildcard
	}
	wcLen := len(wc)
	n := len(pattern)
	var out strings.Builder
	out.Grow(n + 16)

	i := 0
	for i < n {
		ch := pattern[i]

		if ch == '\\' {
			if i+1 < n {
				out.WriteByte(ch)
				out.WriteByte(pattern[i+1])
				i += 2
			} else {
				out.WriteByte(ch)
				i++
			}
			continue
		}

		// Literal group (...) - never expand shortcuts inside literal groups.
		if ch == '(' {
			out.WriteByte(ch)
			i++
			for i < n {
				if pattern[i] == '\\' && i+1 < n {
					out.WriteByte(pattern[i])
					out.WriteByte(pattern[i+1])
					i += 2
				} else if pattern[i] == ')' {
					out.WriteByte(pattern[i])
					i++
					break
				} else {
					out.WriteByte(pattern[i])
					i++
				}
			}
			continue
		}

		// Bracketed class [...] - expand shortcuts inline.
		if ch == '[' {
			out.WriteByte(ch)
			i++
			for i < n {
				if pattern[i] == '\\' && i+1 < n {
					out.WriteByte(pattern[i])
					out.WriteByte(pattern[i+1])
					i += 2
				} else if pattern[i] == ']' {
					out.WriteByte(pattern[i])
					i++
					break
				} else if i+wcLen < n && pattern[i:i+wcLen] == wc {
					key := pattern[i+wcLen]
					if expanded, ok := CharClasses[key]; ok {
						out.WriteString(expanded)
						i += wcLen + 1
					} else {
						out.WriteByte(pattern[i])
						i++
					}
				} else {
					out.WriteByte(pattern[i])
					i++
				}
			}
			continue
		}

		// Outside brackets.
		if i+wcLen < n && pattern[i:i+wcLen] == wc {
			key := pattern[i+wcLen]
			if expanded, ok := CharClasses[key]; ok {
				out.WriteByte('[')
				out.WriteString(expanded)
				out.WriteByte(']')
				i += wcLen + 1
				continue
			}
		}

		out.WriteByte(ch)
		i++
	}

	return out.String()
}

// FormatExpression processes positional file/inline arguments against a pattern.
// Arguments prefixed with "//" are treated as inline sub-expressions and substituted
// into their corresponding unescaped '^' placeholder position; remaining arguments
// are returned as file paths for FileNode binding.
func FormatExpression(expression string, files []string) (string, []string) {
	if len(files) == 0 {
		return expression, []string{}
	}

	filesOut := make([]string, 0, len(files))
	var out strings.Builder
	out.Grow(len(expression) + 32)

	n := len(expression)
	argIdx := 0
	inSquare := false
	inParen := false

	for i := 0; i < n; {
		ch := expression[i]
		if ch == '\\' {
			out.WriteByte(ch)
			if i+1 < n {
				out.WriteByte(expression[i+1])
				i += 2
			} else {
				i++
			}
			continue
		}

		if !inParen {
			if ch == '[' && !inSquare {
				inSquare = true
				out.WriteByte(ch)
				i++
				continue
			}
			if ch == ']' && inSquare {
				inSquare = false
				out.WriteByte(ch)
				i++
				continue
			}
		}
		if !inSquare {
			if ch == '(' && !inParen {
				inParen = true
				out.WriteByte(ch)
				i++
				continue
			}
			if ch == ')' && inParen {
				inParen = false
				out.WriteByte(ch)
				i++
				continue
			}
		}

		if ch == '^' && !inSquare && !inParen && argIdx < len(files) {
			arg := files[argIdx]
			argIdx++
			if strings.HasPrefix(arg, "//") {
				out.WriteString(arg[2:])
			} else {
				out.WriteByte('^')
				filesOut = append(filesOut, arg)
			}
			i++
			continue
		}

		out.WriteByte(ch)
		i++
	}

	// Append any remaining non-inline file arguments (e.g. if user passed extra files or expression has no carets yet).
	for ; argIdx < len(files); argIdx++ {
		arg := files[argIdx]
		if !strings.HasPrefix(arg, "//") {
			filesOut = append(filesOut, arg)
		}
	}

	return out.String(), filesOut
}
