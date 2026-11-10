// SPDX-License-Identifier: MIT OR Apache-2.0

package renovatehashes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

const moduleFile = "go/go.mod"

// Refresh updates changed entries and restores all files on failure.
func Refresh(ctx context.Context, root string, entries []Entry, run Runner) error {
	_, err := refresh(ctx, root, entries, run, os.Getenv)
	return err
}

func refresh(
	ctx context.Context,
	root string,
	entries []Entry,
	run Runner,
	getenv func(string) string,
) ([]Entry, error) {
	if run == nil {
		run = execRunner{root: root}
	}
	baseRef, err := baseReference(ctx, run, getenv("RENOVATE_BASE_BRANCH"))
	if err != nil {
		return nil, err
	}
	selected, err := changedEntries(ctx, root, entries, baseRef, run)
	if err != nil || len(selected) == 0 {
		return selected, err
	}
	originals, current, err := snapshotFiles(root, selected)
	if err != nil {
		return nil, err
	}
	for _, entry := range selected {
		text, err := refreshTool(ctx, root, entry, current[entry.File], originals[entry.File].mode, run, getenv)
		if err != nil {
			if ctx.Err() != nil {
				err = failf("interrupted by %s", context.Cause(ctx))
			}
			return nil, errors.Join(err, restoreFiles(root, originals))
		}
		writeErr := writeText(root, entry.File, text, originals[entry.File].mode)
		if writeErr != nil {
			return nil, errors.Join(failf("could not write %s: %s", entry.File, writeErr), restoreFiles(root, originals))
		}
		current[entry.File] = text
	}
	return selected, nil
}

func baseReference(ctx context.Context, run Runner, branch string) (string, error) {
	if branch == "" {
		branch = "main"
	}
	remote := "origin/" + branch
	_, _, exit, err := run.Run(ctx, "git", "rev-parse", "--verify", "-q", remote)
	if err != nil {
		return "", withPrefix(err)
	}
	if exit == 0 {
		return remote, nil
	}
	return branch, nil
}

func changedEntries(
	ctx context.Context,
	root string,
	entries []Entry,
	baseRef string,
	run Runner,
) ([]Entry, error) {
	filesystem, err := os.OpenRoot(root)
	if err != nil {
		return nil, failf("could not open %s: %s", root, err)
	}
	defer func() { _ = filesystem.Close() }()
	baseFiles := make(map[string]string)
	fileChanges := make(map[string]bool)
	selected := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		changed, err := entryFilesChanged(ctx, entry, baseRef, run, fileChanges)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		base, err := entryBaseFile(ctx, entry, baseRef, run, baseFiles)
		if err != nil {
			return nil, err
		}
		current, err := filesystem.ReadFile(filepath.FromSlash(entry.File))
		if err != nil {
			return nil, failf("could not read %s: %s", entry.File, err)
		}
		entryDiffers, err := entryChanged(entry, string(current), base)
		if err != nil {
			return nil, err
		}
		if entry.File == "go/package.nix" && fileChanges[moduleFile] || entryDiffers {
			selected = append(selected, entry)
		}
	}
	return selected, nil
}

func entryFilesChanged(
	ctx context.Context,
	entry Entry,
	baseRef string,
	run Runner,
	fileChanges map[string]bool,
) (bool, error) {
	changed, ok := fileChanges[entry.File]
	if !ok {
		var err error
		changed, err = changedFromBase(ctx, run, baseRef, entry.File)
		if err != nil {
			return false, err
		}
		fileChanges[entry.File] = changed
	}
	if entry.File != "go/package.nix" {
		return changed, nil
	}
	moduleChanged, ok := fileChanges[moduleFile]
	if !ok {
		var err error
		moduleChanged, err = changedFromBase(ctx, run, baseRef, moduleFile)
		if err != nil {
			return false, err
		}
		fileChanges[moduleFile] = moduleChanged
	}
	return changed || moduleChanged, nil
}

func entryBaseFile(
	ctx context.Context,
	entry Entry,
	baseRef string,
	run Runner,
	baseFiles map[string]string,
) (string, error) {
	base, ok := baseFiles[entry.File]
	if ok {
		return base, nil
	}
	base, err := showFile(ctx, run, baseRef, entry.File)
	if err != nil {
		return "", err
	}
	baseFiles[entry.File] = base
	return base, nil
}

func changedFromBase(ctx context.Context, run Runner, baseRef, path string) (bool, error) {
	_, stderr, exit, err := run.Run(ctx, "git", "diff", "--quiet", baseRef, "--", path)
	if err != nil {
		return false, withPrefix(err)
	}
	if exit > 1 {
		return false, failf("could not compare %s with %s: %s", path, baseRef, stderr)
	}
	return exit != 0, nil
}

func showFile(ctx context.Context, run Runner, baseRef, path string) (string, error) {
	stdout, stderr, exit, err := run.Run(ctx, "git", "show", baseRef+":"+path)
	if err != nil {
		return "", withPrefix(err)
	}
	if exit != 0 {
		return "", failf("could not read %s from %s: %s", path, baseRef, stderr)
	}
	return stdout, nil
}

func snapshotFiles(root string, entries []Entry) (map[string]fileSnapshot, map[string]string, error) {
	filesystem, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, failf("could not open %s: %s", root, err)
	}
	defer func() { _ = filesystem.Close() }()
	originals := make(map[string]fileSnapshot, len(entries))
	current := make(map[string]string, len(entries))
	for _, entry := range entries {
		if _, ok := originals[entry.File]; ok {
			continue
		}
		info, err := filesystem.Stat(filepath.FromSlash(entry.File))
		if err != nil {
			return nil, nil, failf("could not read %s: %s", entry.File, err)
		}
		data, err := filesystem.ReadFile(filepath.FromSlash(entry.File))
		if err != nil {
			return nil, nil, failf("could not read %s: %s", entry.File, err)
		}
		originals[entry.File] = fileSnapshot{data: data, mode: info.Mode().Perm()}
		current[entry.File] = string(data)
	}
	return originals, current, nil
}

func entryChanged(entry Entry, current, base string) (bool, error) {
	if _, hasSource := entry.SourceAttr.Get(); hasSource {
		currentBlock, err := blockFor(current, entry)
		if err != nil {
			return false, err
		}
		baseBlock, err := blockFor(base, entry)
		if err != nil {
			return false, err
		}
		return currentBlock != baseBlock, nil
	}
	currentHash, currentOK := fileHashValue(current, "vendorHash")
	baseHash, baseOK := fileHashValue(base, "vendorHash")
	return !currentOK || !baseOK || currentHash != baseHash, nil
}
