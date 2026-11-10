// SPDX-License-Identifier: MIT OR Apache-2.0

package inventory

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

// Inventory measures tracked source files and direct tracked files per directory.
func Inventory(paths []string, cfg model.Config, repository *os.Root) (model.Snapshot, error) {
	files := map[string]int{}
	dirs := map[string]int{}
	for _, path := range paths {
		if err := inventoryPath(path, cfg, repository, files, dirs); err != nil {
			return model.Snapshot{}, err
		}
	}
	return model.Snapshot{Files: files, Dirs: dirs}, nil
}

func inventoryPath(path string, cfg model.Config, repository *os.Root, files, dirs map[string]int) error {
	trackedPath := filepath.ToSlash(path)
	if MatchesAny(trackedPath, cfg.Exclude) {
		return nil
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(trackedPath)))
	if parent == "." {
		parent = ""
	}
	dirs[parent]++
	if !CountedPath(trackedPath, cfg) {
		return nil
	}
	file, err := repository.Open(filepath.FromSlash(trackedPath))
	if err != nil {
		return fmt.Errorf("open tracked file %q: %w", trackedPath, err)
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
		return errors.Join(countErr, closeErr)
	}
	if !generated {
		files[trackedPath] = lines
	}
	return nil
}

// CountedPath reports whether a path participates in configured source measurements.
func CountedPath(path string, cfg model.Config) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "testdata" || segment == "fixtures" {
			return false
		}
	}
	return slices.Contains(cfg.Extensions, filepath.Ext(path)) && !MatchesAny(path, cfg.Exclude)
}

// MatchesAny reports whether a repository path matches one configured glob.
func MatchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if MatchGlob(pattern, path) {
			return true
		}
	}
	return false
}

// MatchGlob matches a slash-separated repository path against a glob.
func MatchGlob(pattern, path string) bool {
	return matchGlobParts(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/"))
}

func matchGlobParts(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		return matchGlobParts(pattern[1:], path) || len(path) > 0 && matchGlobParts(pattern, path[1:])
	}
	if len(path) == 0 {
		return false
	}
	matched, err := filepath.Match(pattern[0], path[0])
	return err == nil && matched && matchGlobParts(pattern[1:], path[1:])
}

func physicalLines(source io.Reader) (int, bool, error) {
	const bufferSize = 4096
	const markerOverlap = len("Code generated") - 1
	reader := bufio.NewReaderSize(source, bufferSize)
	var window [bufferSize + markerOverlap]byte
	var carry [markerOverlap]byte
	var carryLen int
	var hasGeneratedMarker, hasCodeGenerated, hasDoNotEdit bool
	lineHasBytes, generated := false, false
	count := 0
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
		body := chunk
		if readErr == nil {
			body = body[:len(body)-1]
		}
		if (readErr == nil || errors.Is(readErr, io.EOF)) && len(body) > 0 && body[len(body)-1] == '\r' {
			body = body[:len(body)-1]
		}
		observe(body)
		if readErr == nil || errors.Is(readErr, io.EOF) {
			if lineHasBytes || readErr == nil {
				count++
				if count <= 5 && (hasGeneratedMarker || hasCodeGenerated && hasDoNotEdit) {
					generated = true
				}
			}
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
