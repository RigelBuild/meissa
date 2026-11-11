// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the dl-claim executable.
package main

import (
	"context"
	"os"

	"github.com/RigelBuild/meissa/go/internal/dlclaim"
)

func main() {
	os.Exit(dlclaim.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
