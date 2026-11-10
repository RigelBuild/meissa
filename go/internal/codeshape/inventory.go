// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	repository, err := os.OpenRoot(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open repository root: %w", err)
	}
	snapshot, inventoryErr := inventoryPaths(repository, output, cfg)
	closeErr := repository.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close repository root: %w", closeErr)
	}
	if inventoryErr != nil || closeErr != nil {
		return Snapshot{}, errors.Join(inventoryErr, closeErr)
	}
	return snapshot, nil
}

func inventoryPaths(repository *os.Root, output []byte, cfg Config) (Snapshot, error) {
	files := map[string]int{}
	dirs := map[string]int{}
	for rawPath := range bytes.SplitSeq(output, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		trackedPath := filepath.ToSlash(string(rawPath))
		if matchesAny(trackedPath, cfg.Exclude) {
			continue
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(trackedPath)))
		if parent == "." {
			parent = ""
		}
		dirs[parent]++
		if !countedPath(trackedPath, cfg) {
			continue
		}
		file, err := repository.Open(filepath.FromSlash(trackedPath))
		if err != nil {
			return Snapshot{}, fmt.Errorf("open tracked file %q: %w", trackedPath, err)
		}
		lines, generated, countErr := physicalLines(file)
		closeErr := file.Close()
		if countErr != nil {
			countErr = fmt.Errorf("count lines in %q: %w", trackedPath, countErr)
		}
		if closeErr != nil {
			closeErr = fmt.Errorf("close tracked file %q: %w", trackedPath, closeErr)
		}
		if countErr != nil || closeErr != nil {
			return Snapshot{}, errors.Join(countErr, closeErr)
		}
		if !generated {
			files[trackedPath] = lines
		}
	}
	return Snapshot{Files: files, Dirs: dirs, Functions: nil}, nil
}

func countedPath(path string, cfg Config) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "testdata" || segment == "fixtures" {
			return false
		}
	}
	extension := filepath.Ext(path)
	if !slices.Contains(cfg.Extensions, extension) {
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

func physicalLines(source io.Reader) (int, bool, error) {
	const bufferSize = 4096
	const markerOverlap = len("Code generated") - 1
	reader := bufio.NewReaderSize(source, bufferSize)
	var window [bufferSize + markerOverlap]byte
	var carry [markerOverlap]byte
	var carryLen int
	var hasGeneratedMarker, hasCodeGenerated, hasDoNotEdit bool
	lineHasBytes := false
	count := 0
	generated := false
	observe := func(chunk []byte) {
		if count >= 5 {
			return
		}
		combined := copy(window[:], carry[:carryLen])
		combined += copy(window[combined:], chunk)
		line := window[:combined]
		hasGeneratedMarker = hasGeneratedMarker || bytes.Contains(line, []byte("@generated"))
		hasCodeGenerated = hasCodeGenerated || bytes.Contains(line, []byte("Code generated"))
		hasDoNotEdit = hasDoNotEdit || bytes.Contains(line, []byte("DO NOT EDIT."))
		carryLen = min(len(line), len(carry))
		copy(carry[:], line[len(line)-carryLen:])
	}
	for {
		chunk, readErr := reader.ReadSlice('\n')
		lineHasBytes = lineHasBytes || len(chunk) > 0
		observe(trimPhysicalLine(chunk, readErr))
		complete := readErr == nil || errors.Is(readErr, io.EOF)
		if complete {
			increment := lineHasBytes || readErr == nil
			count, generated = completePhysicalLine(count, generated, increment,
				hasGeneratedMarker || hasCodeGenerated && hasDoNotEdit)
			carryLen, lineHasBytes = 0, false
			hasGeneratedMarker, hasCodeGenerated, hasDoNotEdit = false, false, false
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			return 0, false, fmt.Errorf("scan source lines: %w", readErr)
		}
	}
	return count, generated, nil
}

func trimPhysicalLine(chunk []byte, readErr error) []byte {
	body := chunk
	if readErr == nil {
		body = body[:len(body)-1]
	}
	if (readErr == nil || errors.Is(readErr, io.EOF)) && len(body) > 0 && body[len(body)-1] == '\r' {
		body = body[:len(body)-1]
	}
	return body
}

func completePhysicalLine(count int, generated, increment, marker bool) (int, bool) {
	if increment {
		count++
		generated = generated || count <= 5 && marker
	}
	return count, generated
}
func trackedPaths(ctx context.Context, root string) ([]string, error) {
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
