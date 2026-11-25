# Go Library (`pkg/gomap`) SDK Guide

In addition to the `gomap` CLI binary, the pattern compiler, analyzer, and zero-allocation generators are exposed as an importable Go package at `github.com/pwnfo/gomap/pkg/gomap`.

## Installation

```bash
go get github.com/pwnfo/gomap@latest
```

## Basic Usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pwnfo/gomap/pkg/gomap"
)

func main() {
	eng := gomap.NewEngine()

	// Compile an expression (with optional external wordlist paths for '^').
	plan, err := eng.Compile("<@env=[dev|prod]>-<@env>-#[01-03]", nil)
	if err != nil {
		panic(err)
	}

	// Query exact arbitrary-precision statistics (*big.Int).
	stats, err := eng.Stats(plan, 1, "", "", false, false)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "Words: %s, Bytes: %s\n", stats.TotalWords, stats.TotalBytes)

	// Stream directly to any io.Writer.
	if err := eng.GenerateToWriter(context.Background(), plan, os.Stdout, "\n", "", "", false, false); err != nil {
		panic(err)
	}
}
```

## High-Throughput Chunk Callback & Indexed Sharding

For maximum zero-allocation throughput or custom distributed sharding, call `GenerateRange` directly:

```go
err := eng.GenerateRange(
	ctx,
	plan,
	stats.StartIndex,           // Global 0-based *big.Int start index
	stats.TotalWords.Uint64(),  // Number of entries to emit
	"\n",                       // Delimiter
	512*1024,                   // Chunk flush threshold in bytes
	func(chunk []byte, words uint64) error {
		// Process chunk before returning (slice is reused on next batch).
		_, err := writer.Write(chunk)
		return err
	},
)
```

## Random Access Lookup (`WordAtIndex`)

```go
word, err := eng.WordAtIndex(plan, big.NewInt(42))
```
