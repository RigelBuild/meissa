// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the ref-gate executable.
package main

import (
	"context"
	"os"

	"github.com/RigelBuild/meissa/go/internal/refgate"
)

func main() {
	os.Exit(refgate.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
