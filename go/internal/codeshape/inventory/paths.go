// SPDX-License-Identifier: MIT OR Apache-2.0

package inventory

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// TrackedPaths returns tracked paths as repository-relative slash-separated names.
func TrackedPaths(ctx context.Context, root string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "ls-files", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	paths := strings.Split(string(output), "\x00")
	if len(paths) > 0 && paths[len(paths)-1] == "" {
		paths = paths[:len(paths)-1]
	}
	return paths, nil
}
