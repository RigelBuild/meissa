// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the Renovate hash refresh command.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RigelBuild/meissa/go/internal/renovatehashes"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		received := <-signals
		switch received {
		case os.Interrupt:
			cancel(renovatehashes.ErrRefreshSIGINT)
		case syscall.SIGTERM:
			cancel(renovatehashes.ErrRefreshSIGTERM)
		}
	}()
	return renovatehashes.Main(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
}
