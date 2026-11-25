package compress

import (
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestCompressedStreamsRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	payload := "alpha\nbeta\ngamma\ndelta\n"

	t.Run("gzip", func(t *testing.T) {
		w, resolved, err := OpenOutput(filepath.Join(tmp, "out.txt"), 4096, FormatGzip, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(resolved) != ".gz" {
			t.Fatalf("resolved = %q, expected .gz", resolved)
		}
		if _, err := io.WriteString(w, payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		// Verify overwrite protection when forceOverwrite == false
		if _, _, err := OpenOutput(resolved, 4096, FormatGzip, nil, false); err == nil {
			t.Fatalf("expected existing file error without --force")
		}

		f, err := os.Open(resolved)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		gr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(gr)
		if err != nil || string(got) != payload {
			t.Fatalf("got %q, err %v", string(got), err)
		}
	})

	t.Run("bzip2", func(t *testing.T) {
		w, resolved, err := OpenOutput(filepath.Join(tmp, "out_bz"), 4096, FormatBzip2, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, payload)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(resolved)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		got, err := io.ReadAll(bzip2.NewReader(f))
		if err != nil || string(got) != payload {
			t.Fatalf("got %q, err %v", string(got), err)
		}
	})

	t.Run("lzma_xz", func(t *testing.T) {
		w, resolved, err := OpenOutput(filepath.Join(tmp, "out_xz"), 4096, FormatLZMA, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, payload)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(resolved)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		xr, err := xz.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(xr)
		if err != nil || string(got) != payload {
			t.Fatalf("got %q, err %v", string(got), err)
		}
	})

	t.Run("zstd", func(t *testing.T) {
		w, resolved, err := OpenOutput(filepath.Join(tmp, "out_zst"), 4096, FormatZstd, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, payload)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(resolved)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		zr, err := zstd.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		got, err := io.ReadAll(zr)
		if err != nil || string(got) != payload {
			t.Fatalf("got %q, err %v", string(got), err)
		}
	})
}
