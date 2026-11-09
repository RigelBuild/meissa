// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Inventory measures tracked source files and direct tracked files per directory.
func Inventory(ctx context.Context, root string, cfg Config) (Snapshot, error) {
	command := exec.CommandContext(ctx, "git", "ls-files", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("git ls-files: %w", err)
	}
	files := map[string]int{}
	dirs := map[string]int{}
	for _, rawPath := range bytes.Split(output, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		trackedPath := filepath.ToSlash(string(rawPath))
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(trackedPath)))
		if parent == "." {
			parent = ""
		}
		dirs[parent]++
		if !countedPath(trackedPath, cfg) {
			continue
		}
		fullPath := filepath.Join(root, filepath.FromSlash(trackedPath))
		file, err := os.Open(fullPath)
		if err != nil {
			return Snapshot{}, fmt.Errorf("open tracked file %q: %w", trackedPath, err)
		}
		lines, generated, countErr := physicalLines(file)
		closeErr := file.Close()
		if countErr != nil {
			return Snapshot{}, fmt.Errorf("count lines in %q: %w", trackedPath, countErr)
		}
		if closeErr != nil {
			return Snapshot{}, fmt.Errorf("close tracked file %q: %w", trackedPath, closeErr)
		}
		if !generated {
			files[trackedPath] = lines
		}
	}
	return Snapshot{
		Files:     files,
		Dirs:      dirs,
		Functions: map[Metric]map[string][]Entry{},
	}, nil
}

func countedPath(path string, cfg Config) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "testdata" || segment == "fixtures" {
			return false
		}
	}
	extension := filepath.Ext(path)
	found := false
	for _, allowed := range cfg.Extensions {
		if extension == allowed {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	return !matchesAny(path, cfg.Exclude)
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	return matchGlobParts(patternParts, pathParts)
}

func matchGlobParts(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		if matchGlobParts(pattern[1:], path) {
			return true
		}
		return len(path) > 0 && matchGlobParts(pattern, path[1:])
	}
	if len(path) == 0 {
		return false
	}
	matched, err := filepath.Match(pattern[0], path[0])
	if err != nil || !matched {
		return false
	}
	return matchGlobParts(pattern[1:], path[1:])
}

func physicalLines(reader io.Reader) (int, bool, error) {
	scanner := bufio.NewScanner(reader)
	count := 0
	generated := false
	for scanner.Scan() {
		count++
		if count <= 5 && (strings.Contains(scanner.Text(), "Code generated") && strings.Contains(scanner.Text(), "DO NOT EDIT.") || strings.Contains(scanner.Text(), "@generated")) {
			generated = true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, false, err
	}
	return count, generated, nil
}
