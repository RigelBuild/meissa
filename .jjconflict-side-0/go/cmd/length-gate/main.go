// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the length-gate executable.
package main

import (
	"context"
	"os"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
)

func main() {
	// The process root context: main is the one place a root context is created.
	os.Exit(lengthgate.Main(context.Background(), os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}
