package script

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pwnfo/gomap/pkg/gomap"
)

// SyntaxError is returned when a .gmap expression script contains invalid directives.
type SyntaxError struct {
	Line    int
	Message string
}

func (e *SyntaxError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("invalid file (line %d): %s", e.Line, e.Message)
	}
	return "invalid file: " + e.Message
}

// Entry represents a single parsed expression and its bound external file paths.
type Entry struct {
	Expression string
	Files      []string
}

type macroDef struct {
	key string
	val string
}

// expandMacro replaces unescaped occurrences of "$<key>;" in line with val.
func expandMacro(line, key, val string) string {
	token := "$" + key + ";"
	if !strings.Contains(line, token) {
		return line
	}

	var out strings.Builder
	out.Grow(len(line) + len(val))
	n := len(line)
	tLen := len(token)

	for i := 0; i < n; {
		if line[i] == '\\' {
			out.WriteByte(line[i])
			if i+1 < n {
				out.WriteByte(line[i+1])
				i += 2
			} else {
				i++
			}
			continue
		}
		if i+tLen <= n && line[i:i+tLen] == token {
			out.WriteString(val)
			i += tLen
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
}

// ParseFile parses a .gmap expression script file (or "-" for stdin).
func ParseFile(path string) ([]Entry, error) {
	if path == "-" {
		return ParseReader(os.Stdin, ".")
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("File %q not found.", path)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("No permission for %q.", path)
		}
		return nil, fmt.Errorf("Failed to open %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory.", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %q: %w", path, err)
	}
	defer f.Close()

	absPath, err := filepath.Abs(path)
	baseDir := "."
	if err == nil {
		baseDir = filepath.Dir(absPath)
	} else {
		baseDir = filepath.Dir(path)
	}

	return ParseReader(f, baseDir)
}

// ParseReader parses script directives (%define, %include, comments, and expressions) from an io.Reader.
func ParseReader(r io.Reader, baseDir string) ([]Entry, error) {
	scanner := bufio.NewScanner(r)
	// Allow long expression lines up to 1MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var defines []macroDef
	var currentFiles []string
	var entries []Entry
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Comments start with "# " or "#\t" or standalone "#" (preserving #[0-255] ranges!).
		if line == "#" || strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "#\t") {
			continue
		}

		// Strip inline comments " # ".
		if idx := strings.Index(line, " # "); idx != -1 {
			line = strings.TrimRight(line[:idx], " \t")
		}
		if line == "" {
			continue
		}

		// Expand previously declared %define macros.
		for _, def := range defines {
			line = expandMacro(line, def.key, def.val)
		}

		fields := strings.Split(line, " ")
		keyword := fields[0]

		if keyword == "%define" {
			// Filter leading empty fields after %define to handle multiple spaces gracefully.
			rest := strings.TrimSpace(line[len("%define"):])
			if rest == "" {
				return nil, &SyntaxError{Line: lineNo, Message: "'%define' keyword requires 2 arguments."}
			}
			spaceIdx := strings.IndexAny(rest, " \t")
			if spaceIdx == -1 {
				return nil, &SyntaxError{Line: lineNo, Message: "'%define' keyword requires 2 arguments."}
			}
			dName := strings.TrimSpace(rest[:spaceIdx])
			dValue := strings.TrimLeft(rest[spaceIdx+1:], " \t")
			if dName == "" || dValue == "" {
				return nil, &SyntaxError{Line: lineNo, Message: "'%define' keyword requires 2 arguments."}
			}
			if strings.ContainsAny(dName, ";$") {
				return nil, &SyntaxError{Line: lineNo, Message: "define name cannot contain ';' or '$'."}
			}
			defines = append(defines, macroDef{key: dName, val: dValue})
			continue
		}

		if keyword == "%include" {
			argStr := strings.TrimSpace(line[len("%include"):])
			if argStr == "" {
				return nil, &SyntaxError{Line: lineNo, Message: "'%include' keyword requires 1 argument."}
			}
			if len(argStr) >= 2 && (argStr[0] == '"' || argStr[0] == '\'') && argStr[len(argStr)-1] == argStr[0] {
				argStr = argStr[1 : len(argStr)-1]
			}

			resolved := argStr
			if !strings.HasPrefix(argStr, "//") {
				if strings.HasPrefix(argStr, "./") || strings.HasPrefix(argStr, "../") {
					resolved = filepath.Clean(filepath.Join(baseDir, argStr))
				} else if !filepath.IsAbs(argStr) {
					// If not found in CWD, check relative to script baseDir as fallback.
					if _, err := os.Stat(argStr); err != nil {
						cand := filepath.Clean(filepath.Join(baseDir, argStr))
						if _, err2 := os.Stat(cand); err2 == nil {
							resolved = cand
						}
					}
				}
			}
			currentFiles = append(currentFiles, resolved)
			continue
		}

		expr, filesOut := gomap.FormatExpression(line, currentFiles)
		entries = append(entries, Entry{
			Expression: expr,
			Files:      filesOut,
		})
		currentFiles = nil
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading script: %w", err)
	}

	return entries, nil
}
