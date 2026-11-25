package format

import (
	"math/big"
	"testing"
	"time"
)

func TestFormatSizeAndParseSize(t *testing.T) {
	if got := FormatSize(500, 0); got != "500 B" {
		t.Errorf("got %q, want 500 B", got)
	}
	if got := FormatSize(1536, 2); got != "1.50 KB" {
		t.Errorf("got %q, want 1.50 KB", got)
	}

	// Test beyond uint64 (1 ZB = 1024^7)
	zb := new(big.Int).Exp(big.NewInt(1024), big.NewInt(7), nil)
	if got := FormatSizeBig(zb, 2); got != "1.00 ZB" {
		t.Errorf("FormatSizeBig(1ZB) = %q, want 1.00 ZB", got)
	}

	parseTests := []struct {
		in   string
		want int64
	}{
		{"100", 100},
		{"100B", 100},
		{"1KB", 1024},
		{"1.5KB", 1536},
		{"  2 mb ", 2 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
	}
	for _, tc := range parseTests {
		got, err := ParseSize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseSize(%q) = (%d, %v), want %d", tc.in, got, err, tc.want)
		}
	}

	if _, err := ParseSize("invalid"); err == nil {
		t.Errorf("expected error for invalid size string")
	}
}

func TestFormatDurationAndHelpers(t *testing.T) {
	if got := FormatDuration(0); got != "0 seconds" {
		t.Errorf("got %q", got)
	}
	if got := FormatDuration(90 * time.Second); got != "1 minutes, 30 seconds" {
		t.Errorf("got %q", got)
	}
	if got := FormatDuration(3661 * time.Second); got != "1 hours, 1 minutes, 1 seconds" {
		t.Errorf("got %q", got)
	}
	if got := FormatBigIntCommas(big.NewInt(12345678)); got != "12,345,678" {
		t.Errorf("got %q, want 12,345,678", got)
	}
	if got := UnescapeDelimiter(`\0\t\n`); got != "\x00\t\n" {
		t.Errorf("UnescapeDelimiter = %q", got)
	}
}
