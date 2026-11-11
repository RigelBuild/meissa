// SPDX-License-Identifier: MIT OR Apache-2.0

// Package codeshape defines code-shape measurements and judgments.
package codeshape

import (
	"errors"
	"fmt"
	"os"
)

// ReadRepositoryFile reads a path confined to the repository root.
func ReadRepositoryFile(root, path string) ([]byte, error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}
	data, readErr := repository.ReadFile(path)
	closeErr := repository.Close()
	return data, errors.Join(readErr, closeErr)
}
