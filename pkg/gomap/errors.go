package gomap

import (
	"fmt"
	"strings"
)

const errorSnippetWindow = 24

// PatternError represents a syntax, semantic, or evaluation error in a gomap pattern.
type PatternError struct {
	Message string
	Pattern string
	Pos     int // 1-based rune/byte position, or <= 0 if not positional
}

// NewPatternError creates a non-positional PatternError.
func NewPatternError(message string) *PatternError {
	return &PatternError{
		Message: message,
		Pos:     -1,
	}
}

// NewPatternErrorAt creates a PatternError pointing to a specific 1-based byte offset.
func NewPatternErrorAt(message, pattern string, pos int) *PatternError {
	return &PatternError{
		Message: message,
		Pattern: pattern,
		Pos:     pos,
	}
}

// Error formats the pattern error with an optional contextual caret snippet.
func (e *PatternError) Error() string {
	msg := e.Message
	if e.Pos >= 0 {
		runes := []rune(e.Pattern)
		if len(runes) > 0 {
			// Convert byte pos (1-based) to rune index safely.
			byteIdx := e.Pos - 1
			if byteIdx < 0 {
				byteIdx = 0
			}
			if byteIdx > len(e.Pattern) {
				byteIdx = len(e.Pattern)
			}
			runeIdx := len([]rune(e.Pattern[:byteIdx]))
			if runeIdx >= len(runes) {
				runeIdx = len(runes) - 1
			}

			start := runeIdx - errorSnippetWindow/2
			if start < 0 {
				start = 0
			}
			end := start + errorSnippetWindow
			if end > len(runes) {
				end = len(runes)
			}
			if end-errorSnippetWindow > 0 && end-start < errorSnippetWindow {
				start = end - errorSnippetWindow
				if start < 0 {
					start = 0
				}
			}

			prefix := ""
			if start > 0 {
				prefix = "... "
			}
			suffix := ""
			if end < len(runes) {
				suffix = " ..."
			}
			snippet := string(runes[start:end])
			caretOffset := len(prefix) + (runeIdx - start)
			if caretOffset < 0 {
				caretOffset = 0
			}

			msg = fmt.Sprintf("%s\n\n%s%s%s\n%s^^^", msg, prefix, snippet, suffix, strings.Repeat(" ", caretOffset))
		} else {
			msg += "\n\n^^^"
		}
	}
	return "[Pattern Error] " + msg
}
