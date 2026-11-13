// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/gitdeps"
)

// TestRunOncePullRequestArguments covers the base, head and merge-mode checks.
func TestRunOncePullRequestArguments(t *testing.T) {
	t.Run("a pull request without base or fetch exits 2", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := pullRequest(t, repository, mo.None[string](), mo.None[string](), "HEAD", false)
		if result.code != 2 {
			t.Fatalf("exit = %d, want 2", result.code)
		}
	})

	t.Run("a non-pull-request event logs a skip and exits 0", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: false, SkipClasses: nil,
		}
		result := runGate(t, repository, env, commentConfig())
		if result.code != 0 || !strings.Contains(result.stdout.String(), "skip") {
			t.Fatalf("exit = %d, stdout %q", result.code, result.stdout.String())
		}
	})

	t.Run("merge mode rejects a head with only one parent", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := pullRequest(t, repository, mo.None[string](), mo.None[string](), "HEAD", true)
		if result.code != 2 || !strings.Contains(result.stderr.String(), "second parent") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("merge mode judges from the head's first parent", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		writeRepositoryFile(t, repository, "src/new.ts", longComment())
		commitChange(t, repository, "add long comment")
		runGit(t, repository, "checkout", "-q", "main")
		runGit(t, repository, "merge", "--no-ff", "-q", "feature", "-m", "merge")
		result := pullRequest(t, repository, mo.None[string](), mo.None[string](), "HEAD", true)
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/new.ts:1:") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}

// TestRunOncePullRequestDiffHardening covers odd files and repository config that must not hide a change.
func TestRunOncePullRequestDiffHardening(t *testing.T) {
	t.Run("empty, binary and non-ASCII files in a diff do not break the run", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n", "old.png": "\x00\x01binary"})
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		writeRepositoryFile(t, repository, "pkg/.gitkeep", "")
		writeRepositoryFile(t, repository, "src/empty.ts", "")
		writeRepositoryFile(t, repository, "img.png", "\x00\x02binary")
		writeRepositoryFile(t, repository, "src/café.ts", longComment())
		err := os.Remove(filepath.Join(repository, "old.png"))
		if err != nil {
			t.Fatal(err)
		}
		commitChange(t, repository, "odd files")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/café.ts:1:") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("repository diff config cannot hide a change", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "config", "diff.external", "true")
		runGit(t, repository, "config", "diff.srcPrefix", "x/")
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		writeRepositoryFile(t, repository, "src/new.ts", longComment())
		commitChange(t, repository, "add long comment")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/new.ts:1:") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("diff.suppressBlankEmpty cannot drop an added marker after a blank line", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/a.ts": "const a = 1;\n\nconst b = 2;\n"})
		runGit(t, repository, "config", "diff.suppressBlankEmpty", "true")
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		marked := "// LENGTH: frame header bit layout\n" + longComment()
		writeRepositoryFile(t, repository, "src/a.ts", "const a = 1;\n\nconst b = 2;\n"+marked)
		commitChange(t, repository, "add marked block")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
		writeRepositoryFile(t, repository, "src/empty.ts", "")
		commitChange(t, repository, "add empty file")
		deps := gitdeps.NewGitDeps(repository, "HEAD")
		diff, err := deps.Diff(t.Context(), "main", "HEAD")
		if err != nil || !strings.Contains(diff, "diff --git a/src/empty.ts") {
			t.Fatalf("diff err=%v, want src/empty.ts in diff; got %q", err, diff)
		}
	})
}

// TestRunOncePullRequestSubmodules covers gitlinks, which have no text to judge.
func TestRunOncePullRequestSubmodules(t *testing.T) {
	t.Run("diff.submodule=diff cannot inline a submodule's files into the diff", func(t *testing.T) {
		submodule := trackedRepository(t, map[string]string{"lib.ts": "clean\n"})
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submodule, "vendor/sub")
		commitChange(t, repository, "add submodule")
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		writeRepositoryFile(t, submodule, "lib.ts", longComment())
		commitChange(t, submodule, "long comment")
		runGit(t, filepath.Join(repository, "vendor/sub"), "-c", "protocol.file.allow=always", "pull", "-q", "origin", "main")
		commitChange(t, repository, "bump submodule")
		runGit(t, repository, "config", "diff.submodule", "diff")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a gitlink named like a gated file is not read", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		runGit(t, repository, "update-index", "--add", "--cacheinfo",
			"160000,1234567890123456789012345678901234567890,vendor/sub.ts")
		runGit(t, repository, "commit", "-q", "-m", "gitlink")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("readAt treats a path as literal, never as a pathspec", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"lit1.ts": "clean\n"})
		runGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,1234567890123456789012345678901234567890,sub")
		runGit(t, repository, "commit", "-q", "-m", "gitlink")
		deps := gitdeps.NewGitDeps(repository, "HEAD")
		_, found, err := deps.ReadAt(t.Context(), "HEAD", "lit?.ts")
		if err != nil || found {
			t.Fatalf("lit?.ts found=%v err=%v, want not found", found, err)
		}
		_, found, err = deps.ReadAt(t.Context(), "HEAD", ":(top)sub")
		if err != nil || found {
			t.Fatalf(":(top)sub found=%v err=%v, want not found", found, err)
		}
		text, found, err := deps.ReadAt(t.Context(), "HEAD", "sub")
		if err != nil || !found || text != "" {
			t.Fatalf("sub text=%q found=%v err=%v, want empty and found", text, found, err)
		}
	})

	t.Run("a gated file replaced by a gitlink is not read", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n", "vendor/sub.ts": "clean\n"})
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		runGit(t, repository, "rm", "-q", "--cached", "vendor/sub.ts")
		runGit(t, repository, "update-index", "--add", "--cacheinfo",
			"160000,1234567890123456789012345678901234567890,vendor/sub.ts")
		runGit(t, repository, "commit", "-q", "-m", "file to gitlink")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}

// TestRunOncePullRequestShallowFetch covers base discovery in a shallow clone.
func TestRunOncePullRequestShallowFetch(t *testing.T) {
	t.Run("fetches enough shallow history to find the merge base", func(t *testing.T) {
		source := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, source, longComment())
		shallow := cloneShallow(t, source, "1")
		result := pullRequest(t, shallow, mo.None[string](), mo.Some("main"), "HEAD", false)
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/file.ts:1:") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	// Full history can be hundreds of MB; a near merge base needs only a deepen.
	t.Run("finds a near merge base without fetching full history", func(t *testing.T) {
		source := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		appendEmptyCommits(t, source, 250, func(n int) string { return "old " + strconv.Itoa(n) })
		makePullRequest(t, source, longComment())
		shallow := cloneShallow(t, source, "1")
		result := pullRequest(t, shallow, mo.None[string](), mo.Some("main"), "HEAD", false)
		if result.code != 1 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
		if out := runGit(t, shallow, "rev-parse", "--is-shallow-repository"); out != "true" {
			t.Fatalf("is-shallow = %q, want true", out)
		}
	})

	t.Run("never shortens a clone that is already deeper than one step", func(t *testing.T) {
		source := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		appendEmptyCommits(t, source, 400, func(n int) string { return "old " + strconv.Itoa(n) })
		makePullRequest(t, source, longComment())
		shallow := cloneShallow(t, source, "400")
		result := pullRequest(t, shallow, mo.None[string](), mo.Some("main"), "HEAD", false)
		if result.code != 1 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
		runGit(t, shallow, "rev-parse", "--verify", "-q", "HEAD~399")
	})
}

// TestRunOncePullRequestOutcomes covers the exit code and output for each kind of result.
func TestRunOncePullRequestOutcomes(t *testing.T) {
	t.Run("an empty diff exits 0 with nothing changed", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "main", false)
		if result.code != 0 || !strings.Contains(result.stdout.String(), "nothing changed") {
			t.Fatalf("exit = %d, stdout %q", result.code, result.stdout.String())
		}
	})

	t.Run("a violating changed file exits 1 and prints path:line", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, repository, longComment())
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "feature", false)
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/file.ts:1:") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a valid marker lets a changed long comment pass", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, repository, "// LENGTH: explains why this comment remains long\n"+longComment())
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "feature", false)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

}

// TestRunOncePullRequestGitFailures covers base, head and object failures that exit 2 with the cause.
func TestRunOncePullRequestGitFailures(t *testing.T) {
	t.Run("an unresolved base exits 2 with the cause on stderr", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := pullRequest(t, repository, mo.Some("missing-base"), mo.None[string](), "HEAD", false)
		if result.code != 2 || !strings.Contains(result.stderr.String(), "missing-base") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("an unresolved head exits 2 with the cause on stderr", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "missing-head", false)
		if result.code != 2 || !strings.Contains(result.stderr.String(), "missing-head") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a missing merge base exits 2 with the cause on stderr", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "checkout", "--orphan", "unrelated")
		runGit(t, repository, "rm", "-rf", ".")
		writeRepositoryFile(t, repository, "other.ts", "unrelated\n")
		commitChange(t, repository, "unrelated root")
		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "unrelated", false)
		if result.code != 2 || !strings.Contains(result.stderr.String(), "no merge base") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a missing changed-file object exits 2 and names the file", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, repository, longComment())
		deps := gitdeps.NewGitDeps(repository, "feature")
		head, found, err := deps.Resolve(t.Context(), "feature")
		if err != nil || !found {
			t.Fatalf("fixture head did not resolve: found=%v err=%v", found, err)
		}
		wrapped := missingHeadObject{Deps: deps, head: head}
		var stdout, stderr bytes.Buffer
		env := lengthgate.GateEnv{
			Event: "pull_request", Head: "feature", Base: mo.Some("main"), Fetch: mo.None[string](),
			Merge: false, All: false, SkipClasses: nil,
		}
		if code := lengthgate.RunOnce(t.Context(), wrapped, commentConfig(), env, &stdout, &stderr); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(stderr.String(), "src/file.ts") {
			t.Fatalf("stderr %q does not name src/file.ts", stderr.String())
		}
	})
}
