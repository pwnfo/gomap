package runner

import (
	"context"
	"errors"
	"math/big"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pwnfo/gomap/internal/compress"
	"github.com/pwnfo/gomap/internal/format"
	"github.com/pwnfo/gomap/internal/ui"
	"github.com/pwnfo/gomap/pkg/gomap"
)

// Options configures wordlist execution, I/O buffering, concurrency, and compression.
type Options struct {
	OutputPath     string
	BufferSize     int
	FlushThreshold int
	Workers        int
	Delimiter      string
	StartToken     string
	EndToken       string
	HasStart       bool
	HasEnd         bool
	Compression    compress.Format
	CompressLevel  *int
	ForceOverwrite bool
	Quiet          bool
}

type orderedChunk struct {
	seq    uint64
	words  uint64
	bufPtr *[]byte
	err    error
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, os.ErrClosed)
}

// Run executes the compiled Plan according to Options, displaying live progress and timing metrics.
// Returns the process exit status code (0 on success or clean EPIPE, 1 on error/interruption).
func Run(ctx context.Context, engine *gomap.Engine, plan *gomap.Plan, stats gomap.StatsResult, opts Options, log *ui.Logger) int {
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if stats.TotalWords.IsUint64() && stats.TotalWords.Uint64() < uint64(workers) {
		w := int(stats.TotalWords.Uint64())
		if w < 1 {
			w = 1
		}
		workers = w
	}

	flushThreshold := opts.FlushThreshold
	if flushThreshold < 4096 {
		flushThreshold = 4096
	}

	outWriter, _, err := compress.OpenOutput(
		opts.OutputPath,
		opts.BufferSize,
		opts.Compression,
		opts.CompressLevel,
		opts.ForceOverwrite,
		workers,
	)
	if err != nil {
		log.Error("%s", err.Error())
		return 1
	}

	var progressBytes atomic.Uint64
	var progressWords atomic.Uint64
	showProgress := !opts.Quiet && (opts.OutputPath != "" || !ui.IsTerminal(os.Stdout.Fd()))

	var bar *ui.ProgressBar
	if opts.Compression != compress.FormatNone {
		bar = ui.NewWordProgressBar(&progressWords, stats.TotalWords)
	} else {
		bar = ui.NewProgressBar(&progressBytes, stats.TotalBytes)
	}

	log.Info("Started @ %s", time.Now().Format(time.ANSIC))
	if showProgress {
		bar.Start()
	}
	startTime := time.Now()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var genErr error
	if workers == 1 || stats.TotalWords.Sign() == 0 {
		genErr = runSingleWorker(runCtx, engine, plan, stats, opts.Delimiter, flushThreshold, outWriter, &progressBytes, &progressWords)
	} else {
		genErr = runOrderedWorkers(runCtx, engine, plan, stats, workers, opts.Delimiter, flushThreshold, outWriter, &progressBytes, &progressWords)
	}

	closeErr := outWriter.Close()
	bar.Stop()

	if genErr != nil {
		if isBrokenPipe(genErr) {
			return 0
		}
		if errors.Is(genErr, context.Canceled) {
			log.Error("Generation stopped with keyboard interrupt!")
			return 130
		}
		log.Error("%s", genErr.Error())
		return 1
	}
	if closeErr != nil {
		if isBrokenPipe(closeErr) {
			return 0
		}
		log.Error("%s", closeErr.Error())
		return 1
	}

	elapsed := time.Since(startTime)
	if opts.OutputPath == "" && !opts.Quiet && len(opts.Delimiter) > 0 && opts.Delimiter[len(opts.Delimiter)-1] != '\n' {
		_, _ = os.Stdout.WriteString("\n")
	}

	var kwSpeed uint64
	if sec := elapsed.Seconds(); sec > 0 && stats.TotalWords.Sign() > 0 {
		wf, _ := new(big.Float).SetInt(stats.TotalWords).Float64()
		kwSpeed = uint64((wf / sec) / 1000.0)
	}

	log.Info("Finished in %s (~%d kW/s).", format.FormatDuration(elapsed), kwSpeed)
	return 0
}

func runSingleWorker(
	ctx context.Context,
	engine *gomap.Engine,
	plan *gomap.Plan,
	stats gomap.StatsResult,
	delimiter string,
	flushThreshold int,
	outWriter interface{ Write([]byte) (int, error) },
	progressBytes *atomic.Uint64,
	progressWords *atomic.Uint64,
) error {
	if stats.TotalWords.Sign() == 0 {
		return nil
	}

	// Support arbitrarily huge word counts by iterating in uint64-sized macro blocks if needed.
	remWords := new(big.Int).Set(stats.TotalWords)
	curStart := new(big.Int).Set(stats.StartIndex)
	maxBatch := new(big.Int).SetUint64(^uint64(0))

	for remWords.Sign() > 0 {
		var batch uint64
		if remWords.IsUint64() {
			batch = remWords.Uint64()
		} else {
			batch = ^uint64(0)
		}

		err := engine.GenerateRange(ctx, plan, curStart, batch, delimiter, flushThreshold, func(chunk []byte, words uint64) error {
			n, wErr := outWriter.Write(chunk)
			if n > 0 {
				progressBytes.Add(uint64(n))
			}
			if wErr == nil && words > 0 {
				progressWords.Add(words)
			}
			return wErr
		})
		if err != nil {
			return err
		}

		if remWords.IsUint64() {
			break
		}
		remWords.Sub(remWords, maxBatch)
		curStart.Add(curStart, maxBatch)
	}

	return nil
}

// runOrderedWorkers distributes contiguous index ranges across `workers` goroutines
// while reassembling chunks in strict deterministic order on the writer goroutine,
// using a sync.Pool of reusable []byte buffers and bounded window backpressure.
func runOrderedWorkers(
	ctx context.Context,
	engine *gomap.Engine,
	plan *gomap.Plan,
	stats gomap.StatsResult,
	workers int,
	delimiter string,
	flushThreshold int,
	outWriter interface{ Write([]byte) (int, error) },
	progressBytes *atomic.Uint64,
	progressWords *atomic.Uint64,
) error {
	totalWords := stats.TotalWords.Uint64()
	if totalWords == 0 {
		return nil
	}

	// Estimate words per ticket so each ticket produces roughly flushThreshold bytes.
	avgBytes := uint64(8)
	if stats.TotalBytes.IsUint64() && totalWords > 0 {
		avg := stats.TotalBytes.Uint64() / totalWords
		if avg > 0 {
			avgBytes = avg
		}
	}
	wordsPerTicket := uint64(flushThreshold) / avgBytes
	if wordsPerTicket < 512 {
		wordsPerTicket = 512
	}
	if wordsPerTicket > 524288 {
		wordsPerTicket = 524288
	}
	// Ensure enough tickets to keep all workers busy.
	if totalWords/wordsPerTicket < uint64(workers*2) {
		wordsPerTicket = totalWords / uint64(workers*2)
		if wordsPerTicket < 1 {
			wordsPerTicket = 1
		}
	}

	numTickets := (totalWords + wordsPerTicket - 1) / wordsPerTicket
	maxInFlight := workers * 3

	bufCap := flushThreshold + int(avgBytes)*64 + 4096
	bufPool := sync.Pool{
		New: func() any {
			b := make([]byte, 0, bufCap)
			return &b
		},
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Sliding window ticket dispatcher guarantees at most maxInFlight tickets are active,
	// preventing out-of-order memory growth if an earlier ticket takes slightly longer.
	ticketCh := make(chan uint64, workers)
	resultCh := make(chan orderedChunk, maxInFlight)
	slotReleaseCh := make(chan struct{}, maxInFlight)
	for i := 0; i < maxInFlight; i++ {
		slotReleaseCh <- struct{}{}
	}

	go func() {
		defer close(ticketCh)
		for seq := uint64(0); seq < numTickets; seq++ {
			select {
			case <-workCtx.Done():
				return
			case <-slotReleaseCh:
			}
			select {
			case <-workCtx.Done():
				return
			case ticketCh <- seq:
			}
		}
	}()

	var workerWg sync.WaitGroup
	for w := 0; w < workers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for seq := range ticketCh {
				offset := seq * wordsPerTicket
				cnt := wordsPerTicket
				if offset+cnt > totalWords {
					cnt = totalWords - offset
				}

				ticketStart := new(big.Int).Add(stats.StartIndex, new(big.Int).SetUint64(offset))
				bufPtr := bufPool.Get().(*[]byte)
				*bufPtr = (*bufPtr)[:0]

				err := engine.GenerateRange(workCtx, plan, ticketStart, cnt, delimiter, bufCap, func(chunk []byte, _ uint64) error {
					*bufPtr = append(*bufPtr, chunk...)
					return nil
				})

				select {
				case resultCh <- orderedChunk{seq: seq, words: cnt, bufPtr: bufPtr, err: err}:
				case <-workCtx.Done():
					bufPool.Put(bufPtr)
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}

	go func() {
		workerWg.Wait()
		close(resultCh)
	}()

	pending := make(map[uint64]orderedChunk, maxInFlight)
	var expectedSeq uint64
	var firstErr error

	for msg := range resultCh {
		if msg.err != nil {
			if firstErr == nil {
				firstErr = msg.err
				cancel()
			}
			if msg.bufPtr != nil {
				bufPool.Put(msg.bufPtr)
			}
			continue
		}
		if firstErr != nil {
			if msg.bufPtr != nil {
				bufPool.Put(msg.bufPtr)
			}
			continue
		}

		pending[msg.seq] = msg
		for {
			ready, ok := pending[expectedSeq]
			if !ok {
				break
			}
			delete(pending, expectedSeq)
			data := *ready.bufPtr
			if len(data) > 0 {
				n, wErr := outWriter.Write(data)
				if n > 0 {
					progressBytes.Add(uint64(n))
				}
				if wErr != nil {
					firstErr = wErr
					cancel()
					bufPool.Put(ready.bufPtr)
					break
				}
				progressWords.Add(ready.words)
			}
			bufPool.Put(ready.bufPtr)
			expectedSeq++
			select {
			case slotReleaseCh <- struct{}{}:
			default:
			}
		}
	}

	for _, leftover := range pending {
		if leftover.bufPtr != nil {
			bufPool.Put(leftover.bufPtr)
		}
	}

	return firstErr
}
