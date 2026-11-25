package compress

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
	"github.com/ulikunitz/xz"
)

// Format identifies a supported streaming compression algorithm.
type Format string

const (
	FormatNone  Format = ""
	FormatGzip  Format = "gzip"
	FormatBzip2 Format = "bzip2"
	FormatLZMA  Format = "lzma"
	FormatZstd  Format = "zstd"
)

// ParseFormat validates and normalizes a compression format name.
// Accepts "xz" as a convenient alias for "lzma".
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return FormatNone, nil
	case "gzip", "gz":
		return FormatGzip, nil
	case "bzip2", "bz2":
		return FormatBzip2, nil
	case "lzma", "xz":
		return FormatLZMA, nil
	case "zstd", "zst":
		return FormatZstd, nil
	default:
		return FormatNone, fmt.Errorf("unsupported compression format %q (supported: gzip, bzip2, lzma, zstd)", s)
	}
}

// Extension returns the canonical file extension for a compression Format.
func Extension(f Format) string {
	switch f {
	case FormatGzip:
		return ".gz"
	case FormatBzip2:
		return ".bz2"
	case FormatLZMA:
		return ".xz"
	case FormatZstd:
		return FormatZstdExt
	default:
		return ""
	}
}

const FormatZstdExt = ".zst"

// EnsureExtension appends the canonical compression extension to path if missing.
func EnsureExtension(f Format, path string) string {
	ext := Extension(f)
	if ext == "" || path == "" || strings.HasSuffix(strings.ToLower(path), ext) {
		return path
	}
	return path + ext
}

// LevelRange returns the valid (min, max, default) compression levels for a Format.
func LevelRange(f Format) (int, int, int) {
	switch f {
	case FormatGzip:
		return 0, 9, 6
	case FormatBzip2:
		return 1, 9, 9
	case FormatLZMA:
		return 0, 9, 6
	case FormatZstd:
		return 1, 9, 6
	default:
		return 0, 0, 0
	}
}

// ValidateLevel validates an optional compression level for the given Format.
func ValidateLevel(f Format, level *int) (int, error) {
	minL, maxL, defL := LevelRange(f)
	if level == nil {
		return defL, nil
	}
	if *level < minL || *level > maxL {
		return 0, fmt.Errorf("%s: level must be %d-%d.", f, minL, maxL)
	}
	return *level, nil
}

// xzDictCapForLevel maps LZMA/XZ preset levels 0..9 to dictionary sizes.
func xzDictCapForLevel(level int) int {
	switch level {
	case 0:
		return 256 * 1024
	case 1:
		return 1 * 1024 * 1024
	case 2:
		return 2 * 1024 * 1024
	case 3, 4:
		return 4 * 1024 * 1024
	case 5, 6:
		return 8 * 1024 * 1024
	case 7:
		return 16 * 1024 * 1024
	case 8:
		return 32 * 1024 * 1024
	case 9:
		return 64 * 1024 * 1024
	default:
		return 8 * 1024 * 1024
	}
}

func zstdEncoderLevel(level int) zstd.EncoderLevel {
	switch {
	case level <= 2:
		return zstd.SpeedFastest
	case level <= 5:
		return zstd.SpeedDefault
	case level <= 7:
		return zstd.SpeedBetterCompression
	default:
		return zstd.SpeedBestCompression
	}
}

// NewStreamWriter wraps dst in a streaming compressor for the given Format, level, and concurrency.
// Uses parallel block compression (pgzip) for gzip and concurrent zstd encoding when concurrency > 1.
func NewStreamWriter(dst io.Writer, format Format, level *int, concurrency int) (io.WriteCloser, error) {
	lvl, err := ValidateLevel(format, level)
	if err != nil {
		return nil, err
	}
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}
	if concurrency > 64 {
		concurrency = 64
	}

	switch format {
	case FormatGzip:
		if concurrency > 1 {
			pw, err := pgzip.NewWriterLevel(dst, lvl)
			if err != nil {
				return nil, err
			}
			// 256KB blocks across worker goroutines yield strong deflate ratio with high multi-core speed.
			if err := pw.SetConcurrency(256*1024, concurrency); err != nil {
				_ = pw.Close()
				return nil, err
			}
			return pw, nil
		}
		return gzip.NewWriterLevel(dst, lvl)

	case FormatBzip2:
		return bzip2.NewWriter(dst, &bzip2.WriterConfig{Level: lvl})

	case FormatLZMA:
		cfg := xz.WriterConfig{DictCap: xzDictCapForLevel(lvl)}
		return cfg.NewWriter(dst)

	case FormatZstd:
		return zstd.NewWriter(
			dst,
			zstd.WithEncoderLevel(zstdEncoderLevel(lvl)),
			zstd.WithEncoderConcurrency(concurrency),
		)

	default:
		return nil, fmt.Errorf("unsupported compression format %q", format)
	}
}

type stackedWriteCloser struct {
	top     io.Writer
	closers []io.Closer // closed in forward order (innermost compressor -> bufio -> file)
	flusher *bufio.Writer
}

func (s *stackedWriteCloser) Write(p []byte) (int, error) {
	return s.top.Write(p)
}

func (s *stackedWriteCloser) Close() error {
	var firstErr error
	for _, c := range s.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.flusher != nil {
		if err := s.flusher.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type bufioFlushCloser struct {
	bw *bufio.Writer
}

func (b *bufioFlushCloser) Close() error {
	return b.bw.Flush()
}

// DefaultBufferSize is the default I/O write buffer size (1 MB) when -b is not specified.
const DefaultBufferSize = 1024 * 1024

// OpenOutput opens stdout (when path == "") or a target file with buffered I/O and optional streaming compression.
// Optional concurrency specifies the encoder thread count for parallel codecs (gzip via pgzip, zstd).
func OpenOutput(path string, bufferSize int, format Format, level *int, forceOverwrite bool, concurrency ...int) (io.WriteCloser, string, error) {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}

	if path == "" {
		bw := bufio.NewWriterSize(os.Stdout, bufferSize)
		return &stackedWriteCloser{
			top:     bw,
			flusher: bw,
		}, "", nil
	}

	resolvedPath := EnsureExtension(format, path)

	info, statErr := os.Stat(resolvedPath)
	if statErr == nil && info.IsDir() {
		return nil, resolvedPath, fmt.Errorf("%q is a directory.", resolvedPath)
	}

	flags := os.O_WRONLY | os.O_CREATE
	if format != FormatNone && !forceOverwrite {
		// Prevent silently overwriting existing compressed archives unless -fo/-force is given.
		flags |= os.O_EXCL
	} else {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(resolvedPath, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, resolvedPath, fmt.Errorf("%q already exists (use -fo/-force to overwrite).", resolvedPath)
		}
		if errors.Is(err, os.ErrPermission) {
			return nil, resolvedPath, fmt.Errorf("No permission for %q.", resolvedPath)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, resolvedPath, fmt.Errorf("Directory for %q does not exist.", resolvedPath)
		}
		return nil, resolvedPath, fmt.Errorf("Failed to open %q: %w", resolvedPath, err)
	}

	bw := bufio.NewWriterSize(file, bufferSize)

	if format == FormatNone {
		return &stackedWriteCloser{
			top:     bw,
			closers: []io.Closer{&bufioFlushCloser{bw: bw}, file},
		}, resolvedPath, nil
	}

	encThreads := 1
	if len(concurrency) > 0 {
		encThreads = concurrency[0]
	}

	compWriter, err := NewStreamWriter(bw, format, level, encThreads)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(resolvedPath)
		return nil, resolvedPath, err
	}

	return &stackedWriteCloser{
		top:     compWriter,
		closers: []io.Closer{compWriter, &bufioFlushCloser{bw: bw}, file},
	}, resolvedPath, nil
}
