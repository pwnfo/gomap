package gomap

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind identifies the lexical category of a pattern token.
type TokenKind uint8

const (
	TokenLit TokenKind = iota
	TokenClass
	TokenRange
	TokenFile
	TokenQMark
	TokenBraces
	TokenBindDef
	TokenBindRef
)

// String returns the human-readable name of a TokenKind.
func (k TokenKind) String() string {
	switch k {
	case TokenLit:
		return "LIT"
	case TokenClass:
		return "CLASS"
	case TokenRange:
		return "RANGE"
	case TokenFile:
		return "FILE"
	case TokenQMark:
		return "QMARK"
	case TokenBraces:
		return "BRACES"
	case TokenBindDef:
		return "BIND_DEF"
	case TokenBindRef:
		return "BIND_REF"
	default:
		return "UNKNOWN"
	}
}

// RangeSpec describes an arithmetic numeric sequence #[Start-End[:Step]].
type RangeSpec struct {
	Start    int64
	End      int64
	Step     int64
	PadWidth int
	Count    uint64
}

// FormatItem formats the i-th item (0-based) of the numeric range.
func (r RangeSpec) FormatItem(idx uint64) string {
	val := r.Start + int64(idx)*r.Step
	if r.PadWidth > 0 {
		return fmt.Sprintf("%0*d", r.PadWidth, val)
	}
	return strconv.FormatInt(val, 10)
}

// AppendItem appends the i-th item (0-based) of the numeric range to dst.
func (r RangeSpec) AppendItem(dst []byte, idx uint64) []byte {
	val := r.Start + int64(idx)*r.Step
	if r.PadWidth > 0 {
		var tmp [24]byte
		num := strconv.AppendInt(tmp[:0], val, 10)
		for pad := r.PadWidth - len(num); pad > 0; pad-- {
			dst = append(dst, '0')
		}
		return append(dst, num...)
	}
	return strconv.AppendInt(dst, val, 10)
}

// MaterializeThreshold is the maximum range element count materialized upfront as strings.
// Larger ranges (e.g. #[0-4294967295]) are evaluated arithmetically in O(1) memory.
const MaterializeThreshold = 65536

// Token represents a single lexical unit in a pattern expression.
type Token struct {
	Kind    TokenKind
	Lit     string
	Choices []string
	Range   RangeSpec
	MinRep  int
	MaxRep  int
	Name    string
	Inner   []Token
	Pos     int
}

var (
	bracesRe = regexp.MustCompile(`^\{(\d+)(?:\s*,\s*(\d+))?\}`)
	rangeRe  = regexp.MustCompile(`^\s*([0-9]+)\s*-\s*([0-9]+)\s*(?::\s*([+-]?\d+)\s*)?$`)
)

// IsValidIdentifier checks if s is a valid variable binding identifier (matching Python's isidentifier).
func IsValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
		} else {
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

func findClosing(s string, start int, closer byte) int {
	for i := start; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == closer {
			return i
		}
	}
	return -1
}

func findBindingClose(s string, start int) int {
	var depthSquare, depthParen, depthBrace int
	for i := start; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' {
			i++
			continue
		}
		switch ch {
		case '[':
			depthSquare++
		case ']':
			if depthSquare > 0 {
				depthSquare--
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case '>':
			if depthSquare == 0 && depthParen == 0 && depthBrace == 0 {
				return i
			}
		}
	}
	return -1
}

func parseRange(pattern string, startIdx int) (RangeSpec, []string, int, error) {
	endPos := findClosing(pattern, startIdx, ']')
	if endPos == -1 {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Unclosed range (missing ']')", pattern, startIdx)
	}
	inner := pattern[startIdx:endPos]
	m := rangeRe.FindStringSubmatch(inner)
	if m == nil {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Invalid range syntax (expected '#[START-END[:STEP]]')", pattern, startIdx)
	}

	startStr, endStr, stepStr := m[1], m[2], m[3]
	rStart, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || rStart < 0 {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Invalid range start bound", pattern, startIdx)
	}
	rEnd, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || rEnd < 0 {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Invalid range end bound", pattern, startIdx)
	}

	var step int64
	if stepStr != "" {
		step, err = strconv.ParseInt(stepStr, 10, 64)
		if err != nil {
			return RangeSpec{}, nil, 0, NewPatternErrorAt("Invalid range step", pattern, startIdx)
		}
	} else if rStart <= rEnd {
		step = 1
	} else {
		step = -1
	}

	if step == 0 {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Range step cannot be zero", pattern, startIdx)
	}
	if (step > 0 && rStart > rEnd) || (step < 0 && rStart < rEnd) {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Invalid range sequence", pattern, startIdx)
	}

	var count uint64
	if step > 0 {
		count = uint64((rEnd-rStart)/step) + 1
	} else {
		absStep := uint64(-step)
		count = uint64(rStart-rEnd)/absStep + 1
	}
	if count == 0 {
		return RangeSpec{}, nil, 0, NewPatternErrorAt("Range produced no values", pattern, startIdx)
	}

	// Smart zero-padding: if either bound has leading zeros (e.g. #[01-31] or #[000-999]),
	// preserve fixed width padding automatically. Single-digit "0" (e.g. #[0-9]) is not padded.
	padWidth := 0
	hasLeadingZero := (len(startStr) > 1 && startStr[0] == '0') || (len(endStr) > 1 && endStr[0] == '0')
	if hasLeadingZero {
		padWidth = len(startStr)
		if len(endStr) > padWidth {
			padWidth = len(endStr)
		}
	}

	spec := RangeSpec{
		Start:    rStart,
		End:      rEnd,
		Step:     step,
		PadWidth: padWidth,
		Count:    count,
	}

	var choices []string
	if count <= MaterializeThreshold {
		choices = make([]string, count)
		for i := uint64(0); i < count; i++ {
			choices[i] = spec.FormatItem(i)
		}
	}

	return spec, choices, endPos + 1, nil
}

func hasUnescapedPipe(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '|' {
			return true
		}
	}
	return false
}

func parseClass(pattern string, startIdx int, literalMode bool) ([]string, int, error) {
	closer := byte(']')
	if literalMode {
		closer = ')'
	}
	endPos := findClosing(pattern, startIdx, closer)
	if endPos == -1 {
		return nil, 0, NewPatternErrorAt(fmt.Sprintf("Unclosed character class (missing '%c')", closer), pattern, startIdx)
	}
	inner := pattern[startIdx:endPos]
	if inner == "" {
		return nil, 0, NewPatternErrorAt("Empty character class is not allowed", pattern, startIdx)
	}

	if literalMode {
		// Unescape escaped closing parenthesis '\)' or '\\' inside literal group if present,
		// while preserving everything else verbatim.
		var sb strings.Builder
		sb.Grow(len(inner))
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) && (inner[i+1] == ')' || inner[i+1] == '\\') {
				sb.WriteByte(inner[i+1])
				i++
			} else {
				sb.WriteByte(inner[i])
			}
		}
		return []string{sb.String()}, endPos + 1, nil
	}

	if !hasUnescapedPipe(inner) {
		var choices []string
		escape := false
		for _, r := range inner {
			if escape {
				choices = append(choices, string(r))
				escape = false
			} else if r == '\\' {
				escape = true
			} else {
				choices = append(choices, string(r))
			}
		}
		if len(choices) == 0 {
			return nil, 0, NewPatternErrorAt("Invalid character class contents", pattern, startIdx)
		}
		return choices, endPos + 1, nil
	}

	var segments []string
	var buf strings.Builder
	escape := false
	for _, r := range inner {
		if escape {
			buf.WriteRune(r)
			escape = false
		} else if r == '\\' {
			escape = true
		} else if r == '|' {
			segments = append(segments, buf.String())
			buf.Reset()
		} else {
			buf.WriteRune(r)
		}
	}
	segments = append(segments, buf.String())

	choices := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		trimmed := strings.TrimSpace(seg)
		if trimmed != "" {
			choices = append(choices, trimmed)
		} else {
			// Preserve explicit whitespace alternatives like [ |admin] or [/s|admin].
			choices = append(choices, seg)
		}
	}

	if len(choices) == 0 {
		return nil, 0, NewPatternErrorAt("Invalid character class contents", pattern, startIdx)
	}
	return choices, endPos + 1, nil
}

func tokenizeRaw(pattern string) ([]Token, error) {
	n := len(pattern)
	tokens := make([]Token, 0, 8)

	for i := 0; i < n; {
		c := pattern[i]
		if c == '\\' {
			if i+1 >= n {
				return nil, NewPatternErrorAt("Invalid escape sequence (trailing backslash)", pattern, i+1)
			}
			r, size := utf8.DecodeRuneInString(pattern[i+1:])
			tokens = append(tokens, Token{Kind: TokenLit, Lit: string(r), Pos: i + 1})
			i += 1 + size
			continue
		}

		if c == '<' {
			if i+1 < n && pattern[i+1] == '@' {
				end := findBindingClose(pattern, i+2)
				if end == -1 {
					return nil, NewPatternErrorAt("Unclosed binding (missing '>')", pattern, i+1)
				}
				inner := pattern[i+2 : end]
				eqPos := strings.IndexByte(inner, '=')
				if eqPos == -1 {
					name := strings.TrimSpace(inner)
					if !IsValidIdentifier(name) {
						return nil, NewPatternErrorAt(fmt.Sprintf("Invalid binding name %q", name), pattern, i+1)
					}
					tokens = append(tokens, Token{Kind: TokenBindRef, Name: name, Pos: i + 1})
				} else {
					name := strings.TrimSpace(inner[:eqPos])
					expr := inner[eqPos+1:]
					if !IsValidIdentifier(name) {
						return nil, NewPatternErrorAt(fmt.Sprintf("Invalid binding name %q", name), pattern, i+1)
					}
					innerTokens, err := tokenizeRaw(expr)
					if err != nil {
						return nil, err
					}
					tokens = append(tokens, Token{
						Kind:  TokenBindDef,
						Name:  name,
						Inner: innerTokens,
						Pos:   i + 1,
					})
				}
				i = end + 1
				continue
			}
			tokens = append(tokens, Token{Kind: TokenLit, Lit: "<", Pos: i + 1})
			i++
			continue
		}

		if c == '#' {
			if i+1 < n && pattern[i+1] == '[' {
				spec, choices, nextIdx, err := parseRange(pattern, i+2)
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, Token{
					Kind:    TokenRange,
					Choices: choices,
					Range:   spec,
					Pos:     i + 1,
				})
				i = nextIdx
			} else {
				tokens = append(tokens, Token{Kind: TokenLit, Lit: "#", Pos: i + 1})
				i++
			}
			continue
		}

		if c == '(' {
			choices, nextIdx, err := parseClass(pattern, i+1, true)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{Kind: TokenClass, Choices: choices, Pos: i + 1})
			i = nextIdx
			continue
		}

		if c == '[' {
			choices, nextIdx, err := parseClass(pattern, i+1, false)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, Token{Kind: TokenClass, Choices: choices, Pos: i + 1})
			i = nextIdx
			continue
		}

		if c == '?' {
			tokens = append(tokens, Token{Kind: TokenQMark, Pos: i + 1})
			i++
			continue
		}

		if c == '^' {
			tokens = append(tokens, Token{Kind: TokenFile, Pos: i + 1})
			i++
			continue
		}

		if c == '{' {
			m := bracesRe.FindStringSubmatchIndex(pattern[i:])
			if m == nil {
				return nil, NewPatternErrorAt("Invalid repetition syntax", pattern, i+1)
			}
			a, err := strconv.Atoi(pattern[i+m[2] : i+m[3]])
			if err != nil {
				return nil, NewPatternErrorAt("Invalid repetition minimum", pattern, i+1)
			}
			b := a
			if m[4] != -1 {
				b, err = strconv.Atoi(pattern[i+m[4] : i+m[5]])
				if err != nil {
					return nil, NewPatternErrorAt("Invalid repetition maximum", pattern, i+1)
				}
			}
			if a > b {
				return nil, NewPatternErrorAt("Invalid repetition range (min > max)", pattern, i+1)
			}
			tokens = append(tokens, Token{Kind: TokenBraces, MinRep: a, MaxRep: b, Pos: i + 1})
			i += m[1]
			continue
		}

		r, size := utf8.DecodeRuneInString(pattern[i:])
		tokens = append(tokens, Token{Kind: TokenLit, Lit: string(r), Pos: i + 1})
		i += size
	}

	return tokens, nil
}

// SplitExpressions splits a pattern string on top-level unescaped '||' operators,
// respecting nested [...], (...), {...}, and <@...> constructs without expanding '/' shortcuts.
func SplitExpressions(pattern string) []string {
	var expressions []string
	var buf strings.Builder
	buf.Grow(len(pattern))

	var depthSquare, depthParen, depthBrace, depthBind int
	n := len(pattern)

	for i := 0; i < n; {
		ch := pattern[i]
		if ch == '\\' {
			buf.WriteByte(ch)
			if i+1 < n {
				buf.WriteByte(pattern[i+1])
				i += 2
			} else {
				i++
			}
			continue
		}

		switch ch {
		case '[':
			depthSquare++
		case ']':
			if depthSquare > 0 {
				depthSquare--
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case '<':
			if i+1 < n && pattern[i+1] == '@' {
				depthBind++
			}
		case '>':
			if depthBind > 0 && depthSquare == 0 && depthParen == 0 && depthBrace == 0 {
				depthBind--
			}
		case '|':
			if i+1 < n && pattern[i+1] == '|' && depthSquare == 0 && depthParen == 0 && depthBrace == 0 && depthBind == 0 {
				expressions = append(expressions, buf.String())
				buf.Reset()
				i += 2
				continue
			}
		}

		buf.WriteByte(ch)
		i++
	}
	expressions = append(expressions, buf.String())
	return expressions
}

// Tokenize expands built-in character classes, splits top-level '||' alternations,
// and tokenizes each sub-expression into a slice of Tokens.
func (g *Engine) Tokenize(pattern string) ([][]Token, error) {
	pr := ExpandShortcuts(pattern, g.Wildcard)
	expressions := SplitExpressions(pr)

	result := make([][]Token, len(expressions))
	for idx, expr := range expressions {
		toks, err := tokenizeRaw(expr)
		if err != nil {
			return nil, err
		}
		result[idx] = toks
	}
	return result, nil
}
