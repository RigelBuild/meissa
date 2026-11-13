// SPDX-License-Identifier: MIT OR Apache-2.0

package gitdeps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDepsReadDiffAndList(t *testing.T) {
	repository := gitRepository(t)
	deps := NewGitDeps(repository, "HEAD")
	text, found, err := deps.ReadAt(t.Context(), "HEAD^", "file.ts")
	if err != nil || !found || text != "base\n" {
		t.Fatalf("ReadAt = (%q, %t, %v), want base text", text, found, err)
	}
	files, err := deps.ListFiles(t.Context(), "HEAD")
	if err != nil || len(files) != 1 || files[0] != "file.ts" {
		t.Fatalf("ListFiles = (%v, %v), want [file.ts]", files, err)
	}
	diff, err := deps.Diff(t.Context(), "HEAD^", "HEAD")
	if err != nil || !strings.Contains(diff, "diff --git a/file.ts") {
		t.Fatalf("Diff = (%q, %v), want file.ts", diff, err)
	}
}

func TestGitDepsMergeBaseUsesResolvedRevisions(t *testing.T) {
	repository := gitRepository(t)
	deps := NewGitDeps(repository, "HEAD")
	base, err := deps.MergeBase(t.Context(), "HEAD^", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want, err := run(t.Context(), repository, "rev-parse", "HEAD^")
	if err != nil {
		t.Fatal(err)
	}
	if base != want {
		t.Fatalf("MergeBase = %q, want %q", base, want)
	}
}

func TestRunGitReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runGit(ctx, t.TempDir(), "status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runGit error = %v, want context.Canceled", err)
	}
}

func gitRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	err := os.Mkdir(repository, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t.Context(), repository, "init", "-q", "-b", "main")
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(repository, "file.ts"), []byte("base\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t.Context(), repository, "add", "file.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t.Context(), repository, "commit", "-q", "-m", "base")
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(repository, "file.ts"), []byte("head\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t.Context(), repository, "commit", "-qam", "head")
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func run(ctx context.Context, repository string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append(
		[]string{"-C", repository, "-c", "user.name=t", "-c", "user.email=t@t"}, args...,
	)...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
