package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pwnfo/gomap/pkg/gomap"
)

func TestOrderedMultiWorkerDeterministicEquality(t *testing.T) {
	tmp := t.TempDir()
	out1 := filepath.Join(tmp, "single.txt")
	out8 := filepath.Join(tmp, "multi8.txt")

	var outBuf, errBuf bytes.Buffer
	// Complex pattern mixing bindings, quantifiers, ranges, and top-level ||
	pattern := `/a{2}#[01-20]||<@x=[admin|root|guest]>_<@x>_/d{2}`

	code1 := Execute(context.Background(), "gomap", []string{pattern, "-q", "-n", "-w", "1", "-ft", "4KB", "-o", out1}, &outBuf, &errBuf)
	if code1 != 0 {
		t.Fatalf("single worker failed (%d): %s", code1, errBuf.String())
	}

	// Test Go single-dash multi-letter flags (-workers, -flush-threshold, -output, -quiet, -non-interactive)
	code8 := Execute(context.Background(), "gomap", []string{"-workers", "8", "-flush-threshold=4KB", "-output", out8, "-quiet", "-non-interactive", pattern}, &outBuf, &errBuf)
	if code8 != 0 {
		t.Fatalf("multi worker failed (%d): %s", code8, errBuf.String())
	}

	b1, err := os.ReadFile(out1)
	if err != nil {
		t.Fatal(err)
	}
	b8, err := os.ReadFile(out8)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(b1, b8) {
		t.Fatalf("multi-worker output differed from single-worker output! len(b1)=%d, len(b8)=%d", len(b1), len(b8))
	}
}

func TestCLIStatsAndGoSingleDashFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), "gomap", []string{"-st", "[abc][12]", "-start", "b1", "-end", "c1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("stats exit = %d, stderr = %s", code, stderr.String())
	}
	report := stdout.String()
	for _, expected := range []string{
		"Combination Preview",
		"Sample Space",
		"a1, a2 ... c1, c2",
		"Pattern Statistics",
		`Delimiter........... "\n" (1 byte)`,
		"Range Filtering",
		"Filtered Words...... 3 (50.00%)",
		"Entries to generate: 3",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("stats output missing %q:\n%s", expected, report)
		}
	}

	// Literal "\n" (backslash + n) must display as "\\n" (2 bytes), distinct from newline "\n" (1 byte)
	stdout.Reset()
	stderr.Reset()
	if code := Execute(context.Background(), "gomap", []string{"-st", "/d", "-d", `\n`}, &stdout, &stderr); code != 0 {
		t.Fatalf("literal backslash-n stats failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `Delimiter........... "\\n" (2 bytes)`) {
		t.Errorf("expected literal \\\\n (2 bytes), got:\n%s", stdout.String())
	}

	// Test -fo and -zl short flags with compressed output
	tmp := t.TempDir()
	gzPath := filepath.Join(tmp, "words.txt.gz")
	stdout.Reset()
	stderr.Reset()
	if code := Execute(context.Background(), "gomap", []string{"[ab]", "-q", "-n", "-z", "gzip", "-zl", "5", "-o", gzPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("initial compressed write failed: %s", stderr.String())
	}
	// Overwrite with -fo
	if code := Execute(context.Background(), "gomap", []string{"[ab]", "-q", "-n", "-z", "gzip", "-zl", "5", "-fo", "-o", gzPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("overwrite with -fo failed: %s", stderr.String())
	}

	// Verify mutual exclusion error between pattern and -file
	stdout.Reset()
	stderr.Reset()
	if code := Execute(context.Background(), "gomap", []string{"/d", "-file", "foo.gmap"}, &stdout, &stderr); code == 0 {
		t.Errorf("expected non-zero exit when both pattern and -file are provided")
	}
}

func TestResponsiveCombinationTableNeverExceedsWidth(t *testing.T) {
	eng := gomap.NewEngine()
	raw := `#[0-255].#[0-255].#[0-255].#[0-255]||[admin|operator|guest]_#[0001-9999]`
	plan, err := eng.Compile(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := eng.InspectExpressions(plan)
	if err != nil {
		t.Fatal(err)
	}
	rawExprs := gomap.SplitExpressions(raw)

	for _, width := range []int{28, 35, 42, 50, 58, 65, 77, 78, 80, 100, 120} {
		var buf bytes.Buffer
		writeCombinationTable(&buf, rawExprs, summaries, width)
		for lineNo, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
			rc := utf8.RuneCountInString(line)
			if rc > width {
				t.Errorf("width=%d line %d exceeded budget (%d runes): %q", width, lineNo+1, rc, line)
			}
		}
	}
}
