package format

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	sizeUnits = []string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	sizeRe    = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*([KMGTP]?B?)?\s*$`)
)

// FormatSize formats a byte count (float64) into a human-readable binary unit string.
func FormatSize(b float64, decimals int) string {
	if b < 0 {
		b = 0
	}
	for i := 0; i < len(sizeUnits)-1; i++ {
		if b < 1024.0 {
			if i == 0 && decimals == 0 {
				return fmt.Sprintf("%.0f %s", b, sizeUnits[i])
			}
			return fmt.Sprintf("%.*f %s", decimals, b, sizeUnits[i])
		}
		b /= 1024.0
	}
	return fmt.Sprintf("%.*f %s", decimals, b, sizeUnits[len(sizeUnits)-1])
}

// FormatSizeBig formats an arbitrary-precision *big.Int byte count into a human-readable string,
// supporting astronomical pattern sizes (beyond uint64 / Exabytes) without overflow.
func FormatSizeBig(b *big.Int, decimals int) string {
	if b == nil || b.Sign() <= 0 {
		if decimals > 0 {
			return fmt.Sprintf("%.*f B", decimals, 0.0)
		}
		return "0 B"
	}
	if b.IsUint64() && b.Uint64() < 1024 && decimals == 0 {
		return fmt.Sprintf("%d B", b.Uint64())
	}

	bf := new(big.Float).SetPrec(128).SetInt(b)
	kilo := big.NewFloat(1024.0)

	for i := 0; i < len(sizeUnits)-1; i++ {
		if bf.Cmp(kilo) < 0 {
			val, _ := bf.Float64()
			return fmt.Sprintf("%.*f %s", decimals, val, sizeUnits[i])
		}
		bf.Quo(bf, kilo)
	}
	val, _ := bf.Float64()
	return fmt.Sprintf("%.*f %s", decimals, val, sizeUnits[len(sizeUnits)-1])
}

// ParseSize parses a human-readable byte size string (e.g. "512KB", "4MB", "1.5GB", "4096") into bytes.
func ParseSize(input string) (int64, error) {
	upper := strings.ToUpper(strings.TrimSpace(input))
	m := sizeRe.FindStringSubmatch(upper)
	if m == nil {
		return 0, fmt.Errorf("invalid size format: %q", input)
	}

	num, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size number: %q", input)
	}

	var mult float64 = 1
	switch m[2] {
	case "", "B":
		mult = 1
	case "K", "KB":
		mult = 1024
	case "M", "MB":
		mult = 1024 * 1024
	case "G", "GB":
		mult = 1024 * 1024 * 1024
	case "T", "TB":
		mult = 1024 * 1024 * 1024 * 1024
	case "P", "PB":
		mult = 1024 * 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size unit in %q", input)
	}

	return int64(num * mult), nil
}

// FormatDuration formats an elapsed duration into a human-readable summary.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d > 0 && d < time.Second {
		return fmt.Sprintf("%.2f seconds", d.Seconds())
	}
	totalSec := int64(d / time.Second)
	h := totalSec / 3600
	rem := totalSec % 3600
	m := rem / 60
	s := rem % 60

	if h > 0 {
		return fmt.Sprintf("%d hours, %d minutes, %d seconds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%d minutes, %d seconds", m, s)
	}
	return fmt.Sprintf("%d seconds", s)
}

// FormatBigIntCommas formats a *big.Int with comma thousand separators (e.g. "1,234,567").
func FormatBigIntCommas(n *big.Int) string {
	if n == nil {
		return "0"
	}
	s := n.String()
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}

	var out strings.Builder
	out.Grow(len(s) + len(s)/3 + 1)
	if neg {
		out.WriteByte('-')
	}
	firstGroup := len(s) % 3
	if firstGroup > 0 {
		out.WriteString(s[:firstGroup])
	}
	for i := firstGroup; i < len(s); i += 3 {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(s[i : i+3])
	}
	return out.String()
}

// UnescapeDelimiter interprets common C/shell escape sequences (\n, \r, \t, \0, \\) in CLI delimiter strings.
func UnescapeDelimiter(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				out.WriteByte('\n')
				i++
			case 'r':
				out.WriteByte('\r')
				i++
			case 't':
				out.WriteByte('\t')
				i++
			case '0':
				out.WriteByte(0)
				i++
			case '\\':
				out.WriteByte('\\')
				i++
			default:
				out.WriteByte(s[i])
			}
		} else {
			out.WriteByte(s[i])
		}
	}
	return out.String()
}
