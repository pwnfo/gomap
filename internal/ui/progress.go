package ui

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/pwnfo/gomap/internal/format"
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// FormatETA formats remaining seconds as MM:SS or HH:MM:SS.
func FormatETA(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return "--:--"
	}
	total := int64(seconds)
	mins := total / 60
	secs := total % 60
	if mins >= 60 {
		hours := mins / 60
		mins %= 60
		return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
	}
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

// RenderProgressLine formats a single status line clamped to terminal width.
func RenderProgressLine(spinner string, current uint64, total *big.Int, elapsed float64, width int) string {
	var totalFloat float64
	if total != nil && total.Sign() > 0 {
		totalFloat, _ = new(big.Float).SetInt(total).Float64()
	}
	curFloat := float64(current)
	if totalFloat > 0 && curFloat > totalFloat {
		curFloat = totalFloat
	}

	percent := 100.0
	if totalFloat > 0 {
		percent = math.Min((curFloat/totalFloat)*100.0, 100.0)
	}

	speed := 0.0
	if elapsed > 0 {
		speed = curFloat / elapsed
	}

	eta := -1.0
	if speed > 0 && totalFloat > 0 {
		rem := math.Max(totalFloat-curFloat, 0)
		eta = rem / speed
	}

	curBig := new(big.Int).SetUint64(current)
	if total != nil && curBig.Cmp(total) > 0 {
		curBig.Set(total)
	}

	line := fmt.Sprintf(
		"%s [%3.0f%%] %s / %s @ %s/s ETA %s",
		spinner,
		percent,
		format.FormatSizeBig(curBig, 2),
		format.FormatSizeBig(total, 2),
		format.FormatSize(speed, 2),
		FormatETA(eta),
	)

	runeCount := utf8.RuneCountInString(line)
	if width > 0 && runeCount > width {
		runes := []rune(line)
		if width <= 3 {
			return string(runes[:width])
		}
		return string(runes[:width-3]) + "..."
	}
	return line
}

// ProgressBar renders a live non-blocking terminal progress indicator on stderr.
type ProgressBar struct {
	progress *atomic.Uint64
	total    *big.Int
	out      io.Writer
	fd       uintptr
	stopCh   chan struct{}
	doneWg   sync.WaitGroup
	stopOnce sync.Once
	started  bool
}

// NewProgressBar creates a new ProgressBar tracking `progress` out of `total` bytes.
func NewProgressBar(progress *atomic.Uint64, total *big.Int) *ProgressBar {
	return &ProgressBar{
		progress: progress,
		total:    total,
		out:      os.Stderr,
		fd:       os.Stderr.Fd(),
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background refresh loop (if stderr is an interactive terminal).
func (p *ProgressBar) Start() {
	if !IsTerminal(p.fd) {
		return
	}
	p.started = true
	p.doneWg.Add(1)
	_, _ = io.WriteString(p.out, "\033[?25l")

	go func() {
		defer p.doneWg.Done()
		defer func() {
			_, _ = io.WriteString(p.out, "\033[?25h\r\033[2K")
		}()

		start := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		frameIdx := 0
		for {
			width := TerminalWidth(p.fd)
			cur := p.progress.Load()
			elapsed := time.Since(start).Seconds()
			spinner := spinnerFrames[frameIdx%len(spinnerFrames)]
			line := RenderProgressLine(spinner, cur, p.total, elapsed, width)
			_, _ = io.WriteString(p.out, "\r\033[2K"+line)
			frameIdx++

			if p.total != nil && p.total.IsUint64() && cur >= p.total.Uint64() {
				return
			}

			select {
			case <-p.stopCh:
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop halts the background refresh loop and restores terminal cursor state.
func (p *ProgressBar) Stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		if p.started {
			p.doneWg.Wait()
		}
	})
}
