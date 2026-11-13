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

func TestRunOnceAllMode(t *testing.T) {
	t.Run("finds an unchanged long block on both PR and push events", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/old.ts": longComment()})
		for _, event := range []string{"pull_request", "push"} {
			env := lengthgate.GateEnv{
				Event: event, Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
				Merge: false, All: true, SkipClasses: nil,
			}
			result := runGate(t, repository, env, commentConfig(), mo.None[string](), mo.None[string]())
			if result.code != 1 || !strings.Contains(result.stderr.String(), "src/old.ts:1:") {
				t.Fatalf("%s: exit = %d, stderr %q", event, result.code, result.stderr.String())
			}
		}
	})

	t.Run("a clean tree exits 0", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: nil,
		}
		result := runGate(t, repository, env, commentConfig(), mo.None[string](), mo.None[string]())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("a live marker exempts its block under --all", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{
			"src/file.ts": "// LENGTH: frame header bit layout\n" + longComment(),
		})
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: nil,
		}
		result := runGate(t, repository, env, commentConfig(), mo.None[string](), mo.None[string]())
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
		deps := gitdeps.NewGitDeps(repository, nil, nil, "HEAD", mo.None[string](), mo.None[string]())
		files, err := deps.ListFiles(t.Context(), "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Sorted(slices.Values(files)); !slices.Equal(got, []string{"img.png", "src/file.ts"}) {
			t.Fatalf("files = %v, want [img.png src/file.ts]", got)
		}
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: nil,
		}
		result := runGate(t, repository, env, commentConfig(), mo.None[string](), mo.None[string]())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("an excluded long block is not reported", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"vendor/sdk.ts": longComment()})
		config := commentConfig()
		config.Exclude = []string{"vendor/**"}
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: nil,
		}
		result := runGate(t, repository, env, config, mo.None[string](), mo.None[string]())
		if result.code != 0 || result.stderr.Len() != 0 {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})

	t.Run("skipClasses drops design records but keeps over-target specs", func(t *testing.T) {
		design := "# Record\n\n" + repeatedWords("record", 3000) + "\n"
		spec := "# Spec\n\n" + repeatedWords("spec", 1600) + "\n"
		repository := trackedRepository(t, map[string]string{"docs/designs/record.md": design, "docs/specs/spec.md": spec})
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: []string{"design record"},
		}
		result := runGate(t, repository, env, docConfig(), mo.None[string](), mo.None[string]())
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
		env := lengthgate.GateEnv{
			Event: "push", Base: mo.None[string](), Fetch: mo.None[string](), Head: "HEAD",
			Merge: false, All: true, SkipClasses: []string{"unknown"},
		}
		result := runGate(t, repository, env, commentConfig(), mo.None[string](), mo.None[string]())
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
		result := runGate(t, repository, env, commentConfig(), mo.Some("main"), mo.None[string]())
		if result.code != 2 || !strings.Contains(result.stderr.String(), "--all") {
			t.Fatalf("exit = %d, stderr %q", result.code, result.stderr.String())
		}
	})
}
