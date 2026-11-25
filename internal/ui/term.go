package ui

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// DefaultPausePrompt is the interactive confirmation prompt displayed before starting generation.
const DefaultPausePrompt = " \u203a Press Enter to start"

// IsTerminal reports whether the given file descriptor is attached to an interactive terminal.
func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// TerminalWidth returns the current width of the terminal attached to fd, defaulting to 80.
func TerminalWidth(fd uintptr) int {
	w, _, err := term.GetSize(int(fd))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// Pause pauses execution in an interactive terminal until Enter is pressed.
// Returns false if cancelled via Ctrl+C, Ctrl+D, or Esc.
// Automatically returns true without blocking if stdin or stderr is not a TTY.
func Pause(prompt string) bool {
	inFd := int(os.Stdin.Fd())
	errFd := int(os.Stderr.Fd())
	if !term.IsTerminal(inFd) || !term.IsTerminal(errFd) {
		return true
	}
	if prompt == "" {
		prompt = DefaultPausePrompt
	}

	oldState, err := term.MakeRaw(inFd)
	if err != nil {
		return true
	}
	defer func() {
		_ = term.Restore(inFd, oldState)
	}()

	_, _ = fmt.Fprint(os.Stderr, prompt)

	var buf [1]byte
	for {
		n, readErr := os.Stdin.Read(buf[:])
		if readErr != nil || n == 0 {
			_, _ = fmt.Fprint(os.Stderr, "\r\n")
			return false
		}
		switch buf[0] {
		case '\r', '\n':
			_, _ = fmt.Fprint(os.Stderr, "\r\033[K")
			return true
		case 0x03, 0x04, 0x1b, 'q', 'Q': // Ctrl+C, Ctrl+D, Esc, q
			_, _ = fmt.Fprint(os.Stderr, "\r\033[K")
			return false
		}
	}
}
