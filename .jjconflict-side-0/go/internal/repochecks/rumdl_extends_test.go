// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// longParagraph is one sentence that exceeds the default MD013 line width.
const longParagraph = "A paragraph whose length exceeds the default maximum line width " +
	"without violating other default rules. "

func TestSharedRumdlPolicyInheritance(t *testing.T) {
	baseConfig := os.Getenv("RUMDL_BASE_CONFIG")
	if baseConfig == "" {
		t.Fatal("RUMDL_BASE_CONFIG is not set")
	}
	rumdl := os.Getenv("RUMDL_BIN")
	if rumdl == "" {
		t.Fatal("RUMDL_BIN is not set")
	}
	pathRumdl, err := exec.LookPath("rumdl")
	if err != nil {
		t.Fatalf("look up rumdl on PATH: %v (RUMDL_BIN %s)", err, rumdl)
	}
	if pathRumdl != rumdl {
		t.Fatalf("PATH rumdl (%s) does not match RUMDL_BIN", pathRumdl)
	}

	t.Run("extends inherits disabled MD013 for a long heading-less paragraph", func(t *testing.T) {
		// The TOML holds the literal placeholder; rumdl expands it from RUMDL_BASE_CONFIG.
		config := "extends = \"$RUMDL_BASE_CONFIG\"\n\n[MD041]\nenabled = false\n"
		result := checkWithRumdl(t, rumdl, baseConfig, config)
		if result.status != 0 {
			t.Fatalf("rumdl exit = %d, want 0: %s%s", result.status, result.stdout, result.stderr)
		}
	})

	t.Run("the heading-less control without extends reports MD013", func(t *testing.T) {
		result := checkWithRumdl(t, rumdl, baseConfig, "[MD041]\nenabled = false\n")
		if result.status != 1 {
			t.Fatalf("rumdl exit = %d, want 1: %s%s", result.status, result.stdout, result.stderr)
		}
		if output := result.stdout + result.stderr; !strings.Contains(output, "MD013") {
			t.Fatalf("output lacks MD013: %s", output)
		}
	})
}

// checkWithRumdl writes config and a long heading-less paragraph, then runs rumdl check on them.
func checkWithRumdl(t *testing.T, rumdl, baseConfig, config string) processResult {
	t.Helper()
	directory := t.TempDir()
	configPath := filepath.Join(directory, ".rumdl.toml")
	markdownPath := filepath.Join(directory, "headingless.md")
	writeFixtureFile(t, configPath, config)
	writeFixtureFile(t, markdownPath, jstext.TrimRight(strings.Repeat(longParagraph, 2))+"\n")
	env := append(os.Environ(), "RUMDL_BASE_CONFIG="+baseConfig)
	return runProcess(t, directory, env, rumdl, "check", "--no-cache", "--config", configPath, markdownPath)
}
