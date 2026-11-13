// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
)

// TestRunOncePullRequestStderr pins the exact diagnostic text for Git-level failures.
func TestRunOncePullRequestStderr(t *testing.T) {
	t.Run("unrelated histories report the resolved base and head SHAs", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "checkout", "-q", "--orphan", "unrelated")
		runGit(t, repository, "rm", "-rfq", ".")
		writeRepositoryFile(t, repository, "src/other.ts", "clean\n")
		commitChange(t, repository, "unrelated root")
		baseSHA := runGit(t, repository, "rev-parse", "main")
		headSHA := runGit(t, repository, "rev-parse", "HEAD")

		result := pullRequest(t, repository, mo.Some("main"), mo.None[string](), "HEAD", false)
		want := "length-gate: no merge base for " + baseSHA + " and " + headSHA
		if result.code != 2 || strings.TrimRight(result.stderr.String(), "\n") != want {
			t.Fatalf("exit = %d, stderr %q, want exit 2 and stderr %q", result.code, result.stderr.String(), want)
		}
	})

	t.Run("a failed fetch prints the fetch error without a wrapper prefix", func(t *testing.T) {
		source := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, source, "// LENGTH: no\n")
		shallow := cloneShallow(t, source, "1")
		result := pullRequest(t, shallow, mo.None[string](), mo.Some("no-such-branch"), "HEAD", false)
		if result.code != 2 {
			t.Fatalf("exit = %d, stderr %q, want exit 2", result.code, result.stderr.String())
		}
		for line := range strings.SplitSeq(strings.TrimRight(result.stderr.String(), "\n"), "\n") {
			if !strings.HasPrefix(line, "length-gate: ") {
				t.Fatalf("stderr line %q lacks the length-gate prefix", line)
			}
			if strings.Contains(line, "fetch base branch:") || strings.Contains(line, "find merge base:") {
				t.Fatalf("stderr line %q carries a wrapper prefix the TypeScript never printed", line)
			}
		}
	})
}

// TestRunOnceUsesLengthGateStderr keeps the usage path on the lengthgate sentinel.
func TestRunOnceUsesLengthGateStderr(t *testing.T) {
	repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
	env := lengthgate.GateEnv{
		Event:       "pull_request",
		Base:        mo.None[string](),
		Fetch:       mo.None[string](),
		Head:        "HEAD",
		Merge:       false,
		All:         false,
		SkipClasses: nil,
	}
	result := runGate(t, repository, env, commentConfig())
	want := "length-gate: pull_request requires LENGTH_GATE_BASE, LENGTH_GATE_FETCH or LENGTH_GATE_MERGE=1"
	if result.code != 2 || strings.TrimRight(result.stderr.String(), "\n") != want {
		t.Fatalf("exit = %d, stderr %q, want %q", result.code, result.stderr.String(), want)
	}
}
