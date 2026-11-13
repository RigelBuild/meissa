// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/gitdeps"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// commentConfig gates .go-style line comments only, with no document classes.
func commentConfig() model.Config {
	return model.Config{Extensions: []string{".ts"}, Exclude: nil, Docs: nil}
}

// docConfig gates two document classes; the design record may carry a marker and the spec may not.
func docConfig() model.Config {
	return model.Config{
		Extensions: nil, Exclude: nil,
		Docs: []model.DocClass{
			{Name: "spec", Include: []string{"docs/specs/**/*.md"}, Exclude: nil, Target: 1500, MarkerAllowed: false},
			{Name: "design record", Include: []string{"docs/designs/**/*.md"}, Exclude: nil, Target: 2500, MarkerAllowed: true},
		},
	}
}

// longComment is five line comments, long enough to breach the comment ratchet.
func longComment() string {
	return "// line 1\n// line 2\n// line 3\n// line 4\n// line 5\n"
}

// gitEnv isolates every git child from the user's and system config, as the TS suite does.
func gitEnv(t *testing.T) []string {
	t.Helper()
	configDir := t.TempDir()
	global := filepath.Join(configDir, "config")
	err := os.WriteFile(global, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+global,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
}

// runGit runs git in repository and fails the test on a nonzero exit.
func runGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git",
		append([]string{"-C", repository, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	command.Env = gitEnv(t)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func writeRepositoryFile(t *testing.T, repository, path, text string) {
	t.Helper()
	absolute := filepath.Join(repository, path)
	err := os.MkdirAll(filepath.Dir(absolute), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(absolute, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// trackedRepository creates a repository on main whose base commit holds files.
func trackedRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	err := os.Mkdir(repository, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "git", "-C", repository, "init", "-q", "-b", "main")
	command.Env = gitEnv(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	for path, text := range files {
		writeRepositoryFile(t, repository, path, text)
	}
	runGit(t, repository, "add", "-A")
	runGit(t, repository, "commit", "-q", "-m", "base")
	return repository
}

func commitChange(t *testing.T, repository, message string) {
	t.Helper()
	runGit(t, repository, "add", "-A")
	runGit(t, repository, "commit", "-q", "-m", message)
}

// makePullRequest checks out feature, writes src/file.ts, and commits it.
func makePullRequest(t *testing.T, repository, text string) {
	t.Helper()
	runGit(t, repository, "checkout", "-q", "-b", "feature")
	writeRepositoryFile(t, repository, "src/file.ts", text)
	commitChange(t, repository, "feature change")
}

// appendEmptyCommits adds count commits to main with fast-import, so a clone can be shallow.
func appendEmptyCommits(t *testing.T, repository string, count int, message func(int) string) {
	t.Helper()
	parent := runGit(t, repository, "rev-parse", "refs/heads/main")
	var stream strings.Builder
	for n := range count {
		text := message(n)
		timestamp := 1_700_000_000 + n
		stream.WriteString("commit refs/heads/main\n")
		stream.WriteString("mark :" + strconv.Itoa(n+1) + "\n")
		stream.WriteString("author t <t@t> " + strconv.Itoa(timestamp) + " +0000\n")
		stream.WriteString("committer t <t@t> " + strconv.Itoa(timestamp) + " +0000\n")
		stream.WriteString("data " + strconv.Itoa(len(text)) + "\n" + text + "\n")
		if n == 0 {
			stream.WriteString("from " + parent + "\n")
		} else {
			stream.WriteString("from :" + strconv.Itoa(n) + "\n")
		}
		stream.WriteString("\n")
	}
	stream.WriteString("done\n")
	command := exec.CommandContext(t.Context(), "git", "-C", repository, "fast-import")
	command.Env = gitEnv(t)
	command.Stdin = strings.NewReader(stream.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, output)
	}
	runGit(t, repository, "checkout", "-f", "main")
}

// cloneShallow clones the feature branch of source at the given depth into a fresh directory.
func cloneShallow(t *testing.T, source string, depth string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "repo")
	command := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", depth,
		"--branch", "feature", "file://"+source, target)
	command.Env = gitEnv(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone: %v\n%s", err, output)
	}
	return target
}

// runCase runs the gate on repository with the given deps binding and captures both streams.
type runCase struct {
	stdout, stderr bytes.Buffer
	code           int
}

func runGate(t *testing.T, repository string, env lengthgate.GateEnv, config model.Config) runCase {
	t.Helper()
	var result runCase
	deps := gitdeps.NewGitDeps(repository, env.Head)
	result.code = lengthgate.RunOnce(t.Context(), deps, config, env, &result.stdout, &result.stderr)
	return result
}

func pullRequest(t *testing.T, repository string, base, fetch mo.Option[string], head string, merge bool) runCase {
	t.Helper()
	env := lengthgate.GateEnv{
		Event:       "pull_request",
		Head:        head,
		Merge:       merge,
		Base:        base,
		Fetch:       fetch,
		All:         false,
		SkipClasses: nil,
	}
	return runGate(t, repository, env, commentConfig())
}

// missingHeadObject hides the head blob, so the run fails as if the object were absent.
type missingHeadObject struct {
	gitdeps.Deps

	head string
}

func (m missingHeadObject) ReadAt(ctx context.Context, revision, path string) (string, bool, error) {
	if revision == m.head {
		return "", false, nil
	}
	text, found, err := m.Deps.ReadAt(ctx, revision, path)
	if err != nil {
		return "", false, fmt.Errorf("read %s at %s: %w", path, revision, err)
	}
	return text, found, nil
}

// repeatedWords returns count words, each a stem followed by its index.
func repeatedWords(stem string, count int) string {
	words := make([]string, count)
	for index := range count {
		words[index] = stem + strconv.Itoa(index)
	}
	return strings.Join(words, " ")
}
