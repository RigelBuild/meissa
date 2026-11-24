// SPDX-License-Identifier: MIT OR Apache-2.0

// Package npmpublish publishes npm packages listed by release-please.
package npmpublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

var errBadPathsReleased = errors.New("bad paths_released")

// Manifest is the package name and version used for registry lookups and logs.
type Manifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Deps provides filesystem, registry, publishing, and log operations.
type Deps interface {
	Read(path string) (Manifest, error)
	Status(ctx context.Context, url string) (int, error)
	Publish(ctx context.Context, path string) error
	Log(line string)
}

// ParsePaths parses release-please's paths_released JSON list.
func ParsePaths(raw string) ([]string, error) {
	var parsed []any
	err := json.Unmarshal([]byte(raw), &parsed)
	if err != nil || len(parsed) == 0 {
		return nil, errBadPathsReleased
	}
	paths := make([]string, 0, len(parsed))
	for _, item := range parsed {
		path, ok := item.(string)
		if !ok {
			return nil, errBadPathsReleased
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// IsPublished reports whether the exact package version already exists on npm.
func IsPublished(ctx context.Context, manifest Manifest, deps Deps) (bool, error) {
	url := "https://registry.npmjs.org/" + strings.Replace(manifest.Name, "/", "%2f", 1) + "/" + manifest.Version
	status, err := deps.Status(ctx, url)
	if err != nil {
		return false, fmt.Errorf("registry status: %w", err)
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf( //nolint:err113 // Dynamic status preserves the TS diagnostic.
			"%s: registry returned %d",
			url,
			status,
		)
	}
}

// PublishAll publishes each missing version in order and stops on the first error.
func PublishAll(ctx context.Context, paths []string, deps Deps) error {
	for _, path := range paths {
		manifest, err := deps.Read(path)
		if err != nil {
			return fmt.Errorf("read manifest %s: %w", path, err)
		}
		id := manifest.Name + "@" + manifest.Version
		published, err := IsPublished(ctx, manifest, deps)
		if err != nil {
			return err
		}
		if published {
			deps.Log("skip " + id + ": already on npm")
			continue
		}
		publishErr := deps.Publish(ctx, path)
		if publishErr != nil {
			return publishErr //nolint:wrapcheck // Publish returns the exact TypeScript CLI error text.
		}
		deps.Log("published " + id)
	}
	return nil
}

func publishReleased(ctx context.Context, paths []string, deps Deps) error {
	npmPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.HasPrefix(path, "packages/") {
			npmPaths = append(npmPaths, path)
		}
	}
	if len(npmPaths) == 0 {
		deps.Log("skip: no npm package released")
		return nil
	}
	return PublishAll(ctx, npmPaths, deps)
}

// Main runs the npm publish CLI without exiting the process.
func Main(ctx context.Context, _ []string, env func(string) string, stdout, stderr io.Writer) int {
	return runMain(ctx, env, stderr, func(root string) Deps {
		return systemDeps{root: root, stdout: stdout, stderr: stderr}
	})
}

func runMain(ctx context.Context, env func(string) string, stderr io.Writer, depsForRoot func(string) Deps) int {
	root := env("GITHUB_WORKSPACE")
	if root == "" {
		// The fallback is the process cwd; the release workflow sets GITHUB_WORKSPACE.
		var err error
		root, err = os.Getwd()
		if err != nil {
			return reportFailure(stderr, err)
		}
	}

	var paths []string
	var err error
	if env("PUBLISH_ALL") == "true" {
		paths, err = allPackages(root)
	} else {
		paths, err = ParsePaths(env("PATHS_RELEASED"))
	}
	if err != nil {
		return reportFailure(stderr, err)
	}
	publishErr := publishReleased(ctx, paths, depsForRoot(root))
	if publishErr != nil {
		return reportFailure(stderr, publishErr)
	}
	return 0
}

func allPackages(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		return nil, fmt.Errorf("read packages: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.ToSlash(filepath.Join("packages", entry.Name()))
		manifestPath := filepath.Join(root, path, "package.json")
		data, err := os.ReadFile(manifestPath) //nolint:gosec // Path uses one immediate directory entry.
		if err != nil {
			return nil, fmt.Errorf("read %s/package.json: %w", path, err)
		}
		var manifest struct {
			Private bool `json:"private"`
		}
		decodeErr := json.Unmarshal(data, &manifest)
		if decodeErr != nil {
			return nil, fmt.Errorf("parse %s/package.json: %w", path, decodeErr)
		}
		if !manifest.Private {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

type systemDeps struct {
	root   string
	stdout io.Writer
	stderr io.Writer
}

func (d systemDeps) Read(path string) (Manifest, error) {
	manifestPath := filepath.Join(d.root, path, "package.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // Release paths are restricted to packages/.
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s/package.json: %w", path, err)
	}
	var manifest Manifest
	decodeErr := json.Unmarshal(data, &manifest)
	if decodeErr != nil {
		return Manifest{}, fmt.Errorf("parse %s/package.json: %w", path, decodeErr)
	}
	return manifest, nil
}

func (systemDeps) Status(ctx context.Context, url string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("create registry request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("request registry status: %w", err)
	}
	closeErr := response.Body.Close()
	if closeErr != nil {
		return 0, fmt.Errorf("close registry response: %w", closeErr)
	}
	return response.StatusCode, nil
}

func (d systemDeps) Publish(ctx context.Context, path string) error {
	command := exec.CommandContext(ctx, "npm", "publish", "--provenance", "--access", "public")
	command.Dir = filepath.Join(d.root, path)
	command.Stdout = d.stdout
	command.Stderr = d.stderr
	err := command.Run()
	if err != nil {
		return fmt.Errorf("npm publish failed in %s", path) //nolint:err113 // Preserve the TS publish error text.
	}
	return nil
}

func (d systemDeps) Log(line string) {
	_, _ = fmt.Fprintln(d.stdout, line) // Deps.Log cannot return output errors.
}

func reportFailure(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err) // The CLI has no further channel for stderr write failures.
	return 1
}

var _ Deps = systemDeps{root: "", stdout: io.Discard, stderr: io.Discard}
