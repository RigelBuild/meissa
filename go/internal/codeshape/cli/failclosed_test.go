// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeCollector(t *testing.T, name, version, output string, status int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then echo '%s'; exit 0; fi\n"+
		"printf '%%s' '%s'\nexit %d\n", version, output, status)
	err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestBiomeFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		status       int
	}{
		{"truncated", `{"summary":{"changed":1,"diagnosticsNotPrinted":1,"errors":0},"diagnostics":[]}`, 0},
		{"errors", `{"summary":{"changed":1,"diagnosticsNotPrinted":0,"errors":1},"diagnostics":[]}`, 0},
		{"exit", `{"summary":{"changed":1,"diagnosticsNotPrinted":0,"errors":0},"diagnostics":[]}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tsRepo(t, "function f() {}\n")
			fakeCollector(t, "biome", "Version: 2.5.15", tc.output, tc.status)
			code, _, stderr := runGoGate(t, root)
			if code != 2 {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestGoLintFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		status       int
	}{
		{"report error", `{"Issues":[],"Report":{"Error":"broken","Linters":[{"Name":"gocognit","Enabled":true}]}}`, 0},
		{"exit", `{}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := goTestRepo(t, "package p\nfunc F() {}\n")
			fakeCollector(t, "golangci-lint", "golangci-lint has version 2.15.0", tc.output, tc.status)
			code, _, stderr := runGoGate(t, root)
			if code != 2 {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestTwoLargeInitFunctions(t *testing.T) {
	body := strings.Repeat("println(1)\n", 41)
	root := goTestRepo(t, "package p\nfunc init() {\n"+body+"}\nfunc init() {\n"+body+"}\n")
	code, stdout, stderr := runGoGate(t, root)
	if code != 1 || !strings.Contains(stdout, "init#1") || !strings.Contains(stdout, "init#2") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
