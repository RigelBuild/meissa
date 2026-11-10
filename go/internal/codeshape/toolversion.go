// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

const (
	// GolangciVersion is the supported golangci-lint release.
	GolangciVersion = "2.15.0"
	// BiomeVersion is the supported Biome release.
	BiomeVersion = "2.5.15"
)

var errToolVersion = errors.New("unsupported tool version")

// CheckToolVersion verifies the linter release before parsing its output.
func CheckToolVersion(ctx context.Context, binary, version string) error {
	command := exec.CommandContext(ctx, binary, "--version")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read tool version: %w", err)
	}
	if slices.Contains(strings.Fields(string(output)), version) {
		return nil
	}
	return fmt.Errorf("%s: expected %s, got %q: %w", binary, version, strings.TrimSpace(string(output)), errToolVersion)
}
