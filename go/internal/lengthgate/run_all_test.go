// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/samber/mo"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/gitdeps"
)

// allEnv is the --all environment for a run on HEAD with no base.
func allEnv(event string, skipClasses []string) lengthgate.GateEnv {
	return lengthgate.GateEnv{
		Event: event, Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
		Merge: false, All: true, SkipClasses: skipClasses,
	}
}

func TestRunOnceAllModeFindsUnchangedBlocks(t *testing.T) {
	t.Run("finds an unchanged long block on both PR and push events", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/old.ts": longComment()})
		for _, event := range []string{"pull_request", "push"} {
			result := runGate(t, repository, allEnv(event, nil), commentConfig())
			if result.code != 1 || !strings.Contains(result.stderr.String(), "src/old.ts:1:") {
				t.Fatalf("%s: exit = %d, stderr %q", event, result.code, result.stderr.String())
			}
		}
	})

	t.Run("a live marker exempts its block under --all", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{
			"src/file.ts": "// LENGTH: frame header bit layout\n" + longComment(),
		})
		result := runGate(t, repository, allEnv("push", nil), commentConfig())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("an excluded long block is not reported", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"vendor/sdk.ts": longComment()})
		config := commentConfig()
		config.Exclude = []string{"vendor/**"}
		result := runGate(t, repository, allEnv("push", nil), config)
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}

func TestRunOnceAllModeToleratesOddTrees(t *testing.T) {
	t.Run("a clean tree exits 0", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := runGate(t, repository, allEnv("push", nil), commentConfig())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a submodule gitlink and binary files do not break --all", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n", "img.png": "\x00\x01binary"})
		// A gitlink names a commit in another repository, so its object is absent here.
		runGit(
			t, repository, "update-index", "--add", "--cacheinfo",
			"160000,1234567890123456789012345678901234567890,vendor/sub",
		)
		runGit(t, repository, "commit", "-q", "-m", "gitlink")
		deps := gitdeps.NewGitDeps(repository, "HEAD")
		files, err := deps.ListFiles(t.Context(), "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Sorted(slices.Values(files)); !slices.Equal(got, []string{"img.png", "src/file.ts"}) {
			t.Fatalf("files = %v, want [img.png src/file.ts]", got)
		}
		result := runGate(t, repository, allEnv("push", nil), commentConfig())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}

func TestRunOnceAllModeSkipClasses(t *testing.T) {
	t.Run("skipClasses drops design records but keeps over-target specs", func(t *testing.T) {
		design := "# Record\n\n" + repeatedWords("record", 3000) + "\n"
		spec := "# Spec\n\n" + repeatedWords("spec", 1600) + "\n"
		repository := trackedRepository(t, map[string]string{"docs/designs/record.md": design, "docs/specs/spec.md": spec})
		result := runGate(t, repository, allEnv("push", []string{"design record"}), docConfig())
		if result.code != 1 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
		stderr := result.stderr.String()
		if !strings.Contains(stderr, "docs/specs/spec.md") ||
			strings.Contains(stderr, "docs/designs/record.md") {
			t.Fatalf("stderr %q", stderr)
		}
	})

	t.Run("an unknown skipped class exits 2", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		result := runGate(t, repository, allEnv("push", []string{"unknown"}), commentConfig())
		if result.code != 2 || !strings.Contains(result.stderr.String(), "unknown") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("skipClasses without all exits 2", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		env := lengthgate.GateEnv{
			Event: "pull_request", Base: mo.Some("main"), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: false, SkipClasses: []string{"spec"},
		}
		result := runGate(t, repository, env, commentConfig())
		if result.code != 2 || !strings.Contains(result.stderr.String(), "--all") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}
