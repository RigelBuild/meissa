// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codeshape-go-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = os.Setenv("TMPDIR", dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, os.RemoveAll(dir))
		os.Exit(1)
	}
	code := m.Run()
	err = os.RemoveAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
