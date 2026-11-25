package compress

import (
	"context"
	"math"
	"math/big"

	"github.com/pwnfo/gomap/pkg/gomap"
)

const (
	exactMaxRawBytes  = 512 * 1024 // Datasets <= 512KB are compressed completely in RAM for an exact result.
	sliceTargetBytes  = 128 * 1024 // Each stratified sample window collects ~128KB (4 windows = ~512KB total).
	maxWordsPerSample = 65536
)

type countingDiscard struct {
	n uint64
}

func (c *countingDiscard) Write(p []byte) (int, error) {
	c.n += uint64(len(p))
	return len(p), nil
}

// EstimateResult holds the predicted (or exact) compressed output size and empirical compression ratio.
type EstimateResult struct {
	Bytes *big.Int // Predicted or exact compressed byte count
	Ratio float64  // Compressed / Uncompressed ratio in (0, 1+]
	Exact bool     // True when the entire dataset fit inside the in-memory probe
}

// EstimateSize predicts the compressed output size of a compiled Plan before writing to disk.
// For small outputs (<= 512KB), it runs the real codec over the full stream in RAM for an exact byte count.
// For large outputs, it uses O(1) indexed seeking to sample 4 contiguous 128KB windows across the keyspace
// (at 0%, 33%, 66%, and 95%) through the real codec and compression level.
func EstimateSize(
	ctx context.Context,
	engine *gomap.Engine,
	plan *gomap.Plan,
	stats gomap.StatsResult,
	delimiter string,
	format Format,
	level *int,
) (EstimateResult, error) {
	if format == FormatNone || stats.TotalWords.Sign() <= 0 || stats.TotalBytes.Sign() <= 0 {
		return EstimateResult{
			Bytes: big.NewInt(0),
			Ratio: 1.0,
			Exact: true,
		}, nil
	}

	// Case 1: Small dataset (<= 512KB) -> exact in-memory compression!
	if stats.TotalBytes.IsUint64() && stats.TotalBytes.Uint64() <= exactMaxRawBytes && stats.TotalWords.IsUint64() {
		var counter countingDiscard
		zw, err := NewStreamWriter(&counter, format, level, 1)
		if err != nil {
			return EstimateResult{}, err
		}
		err = engine.GenerateRange(ctx, plan, stats.StartIndex, stats.TotalWords.Uint64(), delimiter, 64*1024, func(chunk []byte, _ uint64) error {
			_, wErr := zw.Write(chunk)
			return wErr
		})
		if err != nil {
			_ = zw.Close()
			return EstimateResult{}, err
		}
		if err := zw.Close(); err != nil {
			return EstimateResult{}, err
		}

		rawF := float64(stats.TotalBytes.Uint64())
		ratio := 1.0
		if rawF > 0 {
			ratio = float64(counter.n) / rawF
		}
		return EstimateResult{
			Bytes: new(big.Int).SetUint64(counter.n),
			Ratio: ratio,
			Exact: true,
		}, nil
	}

	// Case 2: Stratified 4-window sampling across [StartIndex, StartIndex + TotalWords).
	// Measure fixed stream framing overhead (header + trailer) on an empty payload.
	var emptyCounter countingDiscard
	emptyWriter, err := NewStreamWriter(&emptyCounter, format, level, 1)
	if err != nil {
		return EstimateResult{}, err
	}
	if err := emptyWriter.Close(); err != nil {
		return EstimateResult{}, err
	}
	overhead := emptyCounter.n

	// Compute average bytes per word to size each sample window to ~128KB.
	tbFloat := new(big.Float).SetPrec(128).SetInt(stats.TotalBytes)
	twFloat := new(big.Float).SetPrec(128).SetInt(stats.TotalWords)
	avgFloat, _ := new(big.Float).Quo(tbFloat, twFloat).Float64()
	if avgFloat < 1.0 {
		avgFloat = 1.0
	}

	wordsPerSlice := uint64(math.Ceil(float64(sliceTargetBytes) / avgFloat))
	if wordsPerSlice < 1 {
		wordsPerSlice = 1
	}
	if wordsPerSlice > maxWordsPerSample {
		wordsPerSlice = maxWordsPerSample
	}

	var sampleCounter countingDiscard
	zw, err := NewStreamWriter(&sampleCounter, format, level, 1)
	if err != nil {
		return EstimateResult{}, err
	}

	endBound := new(big.Int).Add(stats.StartIndex, stats.TotalWords)
	prevEnd := new(big.Int).Set(stats.StartIndex)
	bigHundred := big.NewInt(100)
	var rawFed uint64

	anchorsPct := [4]int64{0, 33, 66, 95}
	for _, pct := range anchorsPct {
		offset := new(big.Int).Mul(stats.TotalWords, big.NewInt(pct))
		offset.Quo(offset, bigHundred)
		anchor := new(big.Int).Add(stats.StartIndex, offset)
		if anchor.Cmp(prevEnd) < 0 {
			anchor.Set(prevEnd)
		}
		if anchor.Cmp(endBound) >= 0 {
			break
		}

		remAvail := new(big.Int).Sub(endBound, anchor)
		take := wordsPerSlice
		if remAvail.IsUint64() && remAvail.Uint64() < take {
			take = remAvail.Uint64()
		}
		if take == 0 {
			continue
		}

		err := engine.GenerateRange(ctx, plan, anchor, take, delimiter, 64*1024, func(chunk []byte, _ uint64) error {
			rawFed += uint64(len(chunk))
			_, wErr := zw.Write(chunk)
			return wErr
		})
		if err != nil {
			_ = zw.Close()
			return EstimateResult{}, err
		}

		prevEnd.Add(anchor, new(big.Int).SetUint64(take))
	}

	if err := zw.Close(); err != nil {
		return EstimateResult{}, err
	}

	if rawFed == 0 {
		return EstimateResult{
			Bytes: new(big.Int).SetUint64(overhead),
			Ratio: 1.0,
			Exact: false,
		}, nil
	}

	var netComp uint64 = 1
	if sampleCounter.n > overhead {
		netComp = sampleCounter.n - overhead
	}
	ratio := float64(netComp) / float64(rawFed)

	// Scale TotalBytes by empirical net ratio and add framing overhead.
	estFloat := new(big.Float).SetPrec(128).Mul(tbFloat, big.NewFloat(ratio))
	estFloat.Add(estFloat, new(big.Float).SetPrec(128).SetUint64(overhead))
	estBig, _ := estFloat.Int(nil)
	if estBig.Sign() <= 0 {
		estBig = big.NewInt(1)
	}

	return EstimateResult{
		Bytes: estBig,
		Ratio: ratio,
		Exact: false,
	}, nil
}
