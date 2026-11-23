// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the npm-publish executable.
package main

import (
	"context"
	"os"

	"github.com/RigelBuild/meissa/go/internal/npmpublish"
)

func main() {
	os.Exit(npmpublish.Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
