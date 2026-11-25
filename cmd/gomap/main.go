package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/pwnfo/gomap/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	prog := "gomap"
	if len(os.Args) > 0 && os.Args[0] != "" {
		prog = filepath.Base(os.Args[0])
	}

	code := cli.Execute(ctx, prog, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
