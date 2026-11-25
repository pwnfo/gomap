package ui

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
)

func TestFormatETAAndProgressLine(t *testing.T) {
	if got := FormatETA(-1); got != "--:--" {
		t.Errorf("got %q, want --:--", got)
	}
	if got := FormatETA(95); got != "01:35" {
		t.Errorf("got %q, want 01:35", got)
	}
	if got := FormatETA(3665); got != "01:01:05" {
		t.Errorf("got %q, want 01:01:05", got)
	}

	line := RenderProgressLine("⠋", 512, big.NewInt(1024), 1.0, 80)
	if !strings.Contains(line, "[ 50%]") {
		t.Errorf("unexpected progress line: %q", line)
	}

	truncated := RenderProgressLine("⠋", 512, big.NewInt(1024), 1.0, 15)
	if !strings.HasSuffix(truncated, "...") {
		t.Errorf("expected truncated line ending with ..., got %q", truncated)
	}

	wordLine := RenderWordProgressLine("⠋", 250000, big.NewInt(1000000), 0.5, 80)
	if !strings.Contains(wordLine, "250.00K / 1.00M words @ 500.00K/s") {
		t.Errorf("unexpected word progress line: %q", wordLine)
	}
}

func TestLogger(t *testing.T) {
	var infoBuf, errBuf bytes.Buffer
	log := &Logger{
		Quiet:   false,
		NoColor: true,
		InfoOut: &infoBuf,
		ErrOut:  &errBuf,
	}
	log.Info("hello %d", 1)
	log.Warn("careful")
	log.Error("boom")

	if !strings.Contains(infoBuf.String(), "hello 1") {
		t.Errorf("missing info log: %q", infoBuf.String())
	}
	if !strings.Contains(errBuf.String(), "WARN: careful") || !strings.Contains(errBuf.String(), "ERROR: boom") {
		t.Errorf("missing warn/error log: %q", errBuf.String())
	}

	// Quiet mode suppresses Info and Warn, keeps Error
	infoBuf.Reset()
	errBuf.Reset()
	log.Quiet = true
	log.Info("hidden")
	log.Warn("hidden")
	log.Error("visible")
	if infoBuf.Len() != 0 || !strings.Contains(errBuf.String(), "ERROR: visible") || strings.Contains(errBuf.String(), "WARN") {
		t.Errorf("quiet mode misbehaved: info=%q err=%q", infoBuf.String(), errBuf.String())
	}
}
