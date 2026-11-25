package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Logger handles structured CLI informational, warning, and error messages.
// Informational messages are written to InfoOut (defaults to stderr so stdout
// remains purely for generated wordlists), and warnings/errors go to ErrOut.
type Logger struct {
	mu      sync.Mutex
	Quiet   bool
	NoColor bool
	InfoOut io.Writer
	ErrOut  io.Writer
}

// NewLogger creates a standard CLI Logger writing human messages to os.Stderr.
func NewLogger(quiet bool) *Logger {
	_, noColor := os.LookupEnv("NO_COLOR")
	return &Logger{
		Quiet:   quiet,
		NoColor: noColor,
		InfoOut: os.Stderr,
		ErrOut:  os.Stderr,
	}
}

// Info prints an informational message unless Quiet is enabled.
func (l *Logger) Info(formatStr string, args ...any) {
	if l.Quiet {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.InfoOut, formatStr+"\n", args...)
}

// Warn prints a warning message unless Quiet is enabled.
func (l *Logger) Warn(formatStr string, args ...any) {
	if l.Quiet {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(formatStr, args...)
	if l.NoColor {
		_, _ = fmt.Fprintf(l.ErrOut, "WARN: %s\n", msg)
	} else {
		_, _ = fmt.Fprintf(l.ErrOut, "\033[1;33mWARN:\033[0m %s\n", msg)
	}
}

// Error prints an error message to ErrOut (always shown, even in Quiet mode).
func (l *Logger) Error(formatStr string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf(formatStr, args...)
	if l.NoColor {
		_, _ = fmt.Fprintf(l.ErrOut, "ERROR: %s\n", msg)
	} else {
		_, _ = fmt.Fprintf(l.ErrOut, "\033[1;31mERROR:\033[0m %s\n", msg)
	}
}
