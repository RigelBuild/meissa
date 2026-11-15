// SPDX-License-Identifier: MIT OR Apache-2.0

package npmpublish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// Main publishes the released npm packages and returns the process exit code.
// It reads GITHUB_WORKSPACE, PUBLISH_ALL and PATHS_RELEASED; any failure exits 1.
func Main(ctx context.Context, _ []string, env func(string) string, stdout, stderr io.Writer) int {
	root := env("GITHUB_WORKSPACE")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return reportFailure(stderr, fmt.Errorf("working directory: %w", err))
		}
		root = cwd
	}
	paths, err := selectPaths(root, env)
	if err != nil {
		return reportFailure(stderr, err)
	}
	deps := osDeps{root: root, stdout: stdout, stderr: stderr}
	err = publishReleased(ctx, paths, deps)
	if err != nil {
		return reportFailure(stderr, err)
	}
	return 0
}

// selectPaths lists every public package on a manual dispatch, else parses paths_released.
func selectPaths(root string, env func(string) string) ([]string, error) {
	if env("PUBLISH_ALL") == "true" {
		return allPackages(root)
	}
	return parsePaths(env("PATHS_RELEASED"))
}

// osDeps performs the real file, registry and npm I/O.
type osDeps struct {
	root   string
	stdout io.Writer
	stderr io.Writer
}

func (d osDeps) Read(path string) (packageManifest, error) {
	var manifest packageManifest
	err := readPackageJSON(d.root, path, &manifest)
	if err != nil {
		return packageManifest{}, err
	}
	return manifest, nil
}

func (osDeps) Status(ctx context.Context, url string) (_ int, err error) {
	ctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	return resp.StatusCode, nil
}

// Publish runs npm publish with provenance in the package directory.
func (d osDeps) Publish(ctx context.Context, path string) error {
	cmd := exec.CommandContext(
		ctx,
		"npm",
		"publish",
		"--provenance",
		"--access",
		"public",
	) //nolint:gosec // G204: fixed npm argv; only the directory varies.
	cmd.Dir = filepath.Join(d.root, path)
	cmd.Stdout = d.stdout
	cmd.Stderr = d.stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("npm publish: %w", err)
	}
	return nil
}

func (d osDeps) Log(line string) {
_, err := fmt.Fprintln(d.stdout, line)
	if err != nil {
		slog.Default().Error("write publish log", "err", err)
	}
}

func reportFailure(stderr io.Writer, err error) int {
_, writeErr := fmt.Fprintf(stderr, "npm-publish: %v\n", err)
	if writeErr != nil {
		slog.Default().Error("write failure report", "err", writeErr)
	}
	return 1
}
