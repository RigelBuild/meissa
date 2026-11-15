// SPDX-License-Identifier: MIT OR Apache-2.0

// Package main implements the design-ledger-gate executable.
package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(Main(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}
