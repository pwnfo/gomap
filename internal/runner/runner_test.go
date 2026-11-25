package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pwnfo/gomap/internal/ui"
	"github.com/pwnfo/gomap/pkg/gomap"
)

func TestRunnerSingleAndMultiWorker(t *testing.T) {
	tmp := t.TempDir()
	eng := gomap.NewEngine()
	plan, err := eng.Compile("user_#[001-250]", nil)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := eng.Stats(plan, 1, "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(tmp, "users.txt")
	log := ui.NewLogger(true)

	code := Run(context.Background(), eng, plan, stats, Options{
		OutputPath:     outPath,
		BufferSize:     64 * 1024,
		FlushThreshold: 4096,
		Workers:        4,
		Delimiter:      "\n",
		Quiet:          true,
	}, log)
	if code != 0 {
		t.Fatalf("Run returned non-zero code %d", code)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 250 || lines[0] != "user_001" || lines[249] != "user_250" {
		t.Fatalf("unexpected lines count=%d first=%q last=%q", len(lines), lines[0], lines[len(lines)-1])
	}
}
