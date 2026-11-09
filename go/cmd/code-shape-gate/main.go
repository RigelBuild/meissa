// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the code-shape-gate executable.
package main

import (
	"context"
	"os"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func main() {
	os.Exit(codeshape.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
