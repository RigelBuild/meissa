// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the npm-publish executable.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RigelBuild/meissa/go/internal/npmpublish"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return npmpublish.Main(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
}
