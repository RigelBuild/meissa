// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
)

// mainRun calls Main in repository with env, returning its exit code and both streams.
func mainRun(t *testing.T, repository string, config string, env map[string]string) runCase {
	t.Helper()
	var result runCase
	full := map[string]string{"GATE_ROOT": repository}
	maps.Copy(full, env)
	lookup := func(key string) (string, bool) {
		value, ok := full[key]
		return value, ok
	}
	argv := []string{"--config", config}
	result.code = lengthgate.Main(t.Context(), argv, lookup, &result.stdout, &result.stderr)
	return result
}

func TestMainMergeModeReadsTheMergeParents(t *testing.T) {
	t.Run("a merge commit with a long comment on its side branch exits 1", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		runGit(t, repository, "checkout", "-q", "-b", "feature")
		writeRepositoryFile(t, repository, "src/new.ts", longComment())
		commitChange(t, repository, "feature change")
		runGit(t, repository, "checkout", "-q", "main")
		writeRepositoryFile(t, repository, "src/main.ts", "clean\n")
		commitChange(t, repository, "main change")
		runGit(t, repository, "merge", "--no-ff", "-q", "feature", "-m", "merge")

		config := writeTestConfig(t, `{"extensions":[".ts"],"exclude":[],"docs":[]}`)
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request",
			"LENGTH_GATE_MERGE": "1",
			"LENGTH_GATE_HEAD":  "HEAD",
		})
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/new.ts:1:") {
			t.Fatalf("exit = %d, stderr %q, want exit 1 naming src/new.ts", result.code, result.stderr.String())
		}
	})

	t.Run("merge mode on a single-parent head exits 2 naming the second parent", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		config := writeTestConfig(t, `{"extensions":[".ts"],"exclude":[],"docs":[]}`)
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request",
			"LENGTH_GATE_MERGE": "1",
			"LENGTH_GATE_HEAD":  "HEAD",
		})
		if result.code != 2 || !strings.Contains(result.stderr.String(), "second parent") {
			t.Fatalf("exit = %d, stderr %q, want exit 2 naming the second parent", result.code, result.stderr.String())
		}
	})
}

func TestMainUsageAndConfigErrors(t *testing.T) {
	repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})

	t.Run("a usage error prints the message and the usage line, exit 2", func(t *testing.T) {
		var result runCase
		lookup := func(string) (string, bool) { return "", false }
		result.code = lengthgate.Main(
			t.Context(), []string{"--all", "--all"}, lookup, &result.stdout, &result.stderr,
		)
		want := "length-gate: --all may appear once\n" +
			"Usage: length-gate --config <path> [--all] [--skip-class <name>]...\n"
		if result.code != 2 || result.stderr.String() != want {
			t.Fatalf("exit = %d, stderr %q, want exit 2 and %q", result.code, result.stderr.String(), want)
		}
	})

	t.Run("a config that does not load exits 2 before any Git work", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.json")
		result := mainRun(t, repository, missing, nil)
		if result.code != 2 || !strings.HasPrefix(result.stderr.String(), "length-gate: Cannot load config") {
			t.Fatalf("exit = %d, stderr %q, want exit 2 naming the config", result.code, result.stderr.String())
		}
	})

	t.Run("a config with a leading negation exits 2 at load", func(t *testing.T) {
		config := writeTestConfig(t, `{"extensions":[],"exclude":["!docs/**"],"docs":[]}`)
		result := mainRun(t, repository, config, nil)
		if result.code != 2 || !strings.Contains(result.stderr.String(), "unsupported leading negation") {
			t.Fatalf("exit = %d, stderr %q, want exit 2 naming the negation", result.code, result.stderr.String())
		}
	})
}

// writeTestConfig writes text to a fresh config file and returns its path.
func writeTestConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "length-gate.json")
	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
