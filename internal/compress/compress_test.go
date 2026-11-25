package compress

import (
	"compress/bzip2"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pwnfo/gomap/pkg/gomap"
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

	t.Run("parallel_pgzip_and_estimate", func(t *testing.T) {
		eng := gomap.NewEngine()
		plan, err := eng.Compile("/a{4}", nil) // 456,976 words (~2.28 MB > 512KB threshold)
		if err != nil {
			t.Fatal(err)
		}
		st, err := eng.Stats(plan, 1, "", "", false, false)
		if err != nil {
			t.Fatal(err)
		}

		est, err := EstimateSize(context.Background(), eng, plan, st, "\n", FormatGzip, nil)
		if err != nil {
			t.Fatal(err)
		}
		if est.Exact {
			t.Errorf("expected sampled estimate (Exact=false) for >512KB dataset")
		}
		if est.Bytes.Sign() <= 0 || est.Bytes.Cmp(st.TotalBytes) >= 0 {
			t.Errorf("unexpected estimate %s vs raw %s", est.Bytes, st.TotalBytes)
		}

		// Write with parallel pgzip (concurrency = 4) and verify standard gzip reader decodes all bytes!
		gzOut := filepath.Join(tmp, "parallel.txt.gz")
		w, _, err := OpenOutput(gzOut, 64*1024, FormatGzip, nil, true, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.GenerateRange(context.Background(), plan, st.StartIndex, st.TotalWords.Uint64(), "\n", 128*1024, func(chunk []byte, _ uint64) error {
			_, wErr := w.Write(chunk)
			return wErr
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		f, err := os.Open(gzOut)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		gr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(gr)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(decoded)) != st.TotalBytes.Int64() {
			t.Fatalf("decoded len = %d, want %s", len(decoded), st.TotalBytes)
		}
	})
}
