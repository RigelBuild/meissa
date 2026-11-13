// SPDX-License-Identifier: MIT OR Apache-2.0

package lengthgate_test

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/lengthgate"
)

// buildCLI compiles cmd/length-gate into a temporary directory and returns the binary path.
func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "length-gate")
	command := exec.CommandContext(t.Context(), "go", "build", "-o", binary,
		"github.com/RigelBuild/meissa/go/cmd/length-gate")
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return binary
}

// writeConfig writes the comment-gate configuration the CLI loads with --config.
func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "length-gate.json")
	const body = `{"extensions":[".ts"],"exclude":["vendor/**"],` +
		`"docs":[{"name":"spec","include":["docs/specs/**/*.md"],"exclude":[],"target":1500,"markerAllowed":false}]}`
	err := os.WriteFile(path, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMainEndToEnd(t *testing.T) {
	t.Run("spawns the CLI and prints a failing finding", func(t *testing.T) {
		binary := buildCLI(t)
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		makePullRequest(t, repository, longComment())
		config := writeConfig(t)
		command := exec.CommandContext(t.Context(), binary, "--config", config)
		command.Dir = repository
		command.Env = append(gitEnv(t),
			"GATE_ROOT="+repository,
			"LENGTH_GATE_EVENT=pull_request",
			"LENGTH_GATE_BASE=main",
			"LENGTH_GATE_HEAD=feature",
		)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		err := command.Run()
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			t.Fatalf("run error = %v, want exit 1", err)
		}
		if !strings.Contains(stderr.String(), "src/file.ts:1:") {
			t.Fatalf("stderr %q does not contain src/file.ts:1:", stderr.String())
		}
	})
}

// mainRun calls Main in repository with env, returning its exit code and both streams.
func mainRun(t *testing.T, repository, config string, env map[string]string, args ...string) runCase {
	t.Helper()
	var result runCase
	full := map[string]string{"GATE_ROOT": repository}
	maps.Copy(full, env)
	lookup := func(key string) (string, bool) {
		value, ok := full[key]
		return value, ok
	}
	argv := append([]string{"--config", config}, args...)
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
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_MERGE": "1", "LENGTH_GATE_HEAD": "HEAD",
		})
		if result.code != 1 || !strings.Contains(result.stderr.String(), "src/new.ts:1:") {
			t.Fatalf("exit = %d, stderr %q, want exit 1 naming src/new.ts", result.code, result.stderr.String())
		}
	})
	t.Run("merge mode on a single-parent head exits 2 naming the second parent", func(t *testing.T) {
		repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
		config := writeTestConfig(t, `{"extensions":[".ts"],"exclude":[],"docs":[]}`)
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_MERGE": "1", "LENGTH_GATE_HEAD": "HEAD",
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
		result.code = lengthgate.Main(t.Context(), []string{"--all", "--all"}, lookup, &result.stdout, &result.stderr)
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

// TestMainRejectsMalformedArguments pins the CLI usage diagnostics.
func TestMainRejectsMalformedArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing config", args: []string{"--all"}, want: "--config is required"},
		{name: "config given twice", args: []string{"--config", "a", "--config", "b"}, want: "--config may appear once"},
		{name: "config without a path", args: []string{"--config"}, want: "--config requires a path"},
		{name: "config followed by another flag", args: []string{"--config", "--all"}, want: "--config requires a path"},
		{name: "all given twice", args: []string{"--config", "a", "--all", "--all"}, want: "--all may appear once"},
		{
			name: "skip-class without a value", args: []string{"--config", "a", "--skip-class"},
			want: "--skip-class requires a class name",
		},
		{
			name: "skip-class with an empty value", args: []string{"--config", "a", "--skip-class", ""},
			want: "--skip-class requires a class name",
		},
		{name: "unknown argument", args: []string{"--config", "a", "--bogus"}, want: `unknown argument "--bogus"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result runCase
			lookup := func(string) (string, bool) { return "", false }
			result.code = lengthgate.Main(t.Context(), tt.args, lookup, &result.stdout, &result.stderr)
			want := "length-gate: " + tt.want + "\n" +
				"Usage: length-gate --config <path> [--all] [--skip-class <name>]...\n"
			if result.code != 2 || result.stderr.String() != want {
				t.Fatalf("exit = %d, stderr %q, want exit 2 and %q", result.code, result.stderr.String(), want)
			}
		})
	}
}

// TestMainUsesEnvironmentValues pins default, empty and merge-mode environment handling.
func TestMainUsesEnvironmentValues(t *testing.T) {
	repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
	config := writeTestConfig(t, `{"extensions":[".ts"],"exclude":[],"docs":[]}`)
	t.Run("an absent head defaults to HEAD", func(t *testing.T) {
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_BASE": "main",
		})
		if result.code != 0 {
			t.Fatalf("exit = %d, stderr %q, want default HEAD to pass", result.code, result.stderr.String())
		}
	})
	t.Run("an explicitly empty head stays empty", func(t *testing.T) {
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_BASE": "main", "LENGTH_GATE_HEAD": "",
		})
		if result.code != 2 || !strings.Contains(result.stderr.String(), `head revision "" does not resolve`) {
			t.Fatalf("exit = %d, stderr %q, want an unresolved empty head", result.code, result.stderr.String())
		}
	})
	t.Run("an explicitly empty base stays set", func(t *testing.T) {
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_BASE": "", "LENGTH_GATE_HEAD": "HEAD",
		})
		if result.code != 2 || !strings.Contains(result.stderr.String(), `base revision "" does not resolve`) {
			t.Fatalf("exit = %d, stderr %q, want an unresolved empty base", result.code, result.stderr.String())
		}
	})
}

// TestMainMergeFlagAndEvent pins the exact merge flag value and event skip behavior.
func TestMainMergeFlagAndEvent(t *testing.T) {
	repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
	config := writeTestConfig(t, `{"extensions":[".ts"],"exclude":[],"docs":[]}`)
	t.Run("merge is enabled only by one", func(t *testing.T) {
		result := mainRun(t, repository, config, map[string]string{
			"LENGTH_GATE_EVENT": "pull_request", "LENGTH_GATE_MERGE": "true", "LENGTH_GATE_HEAD": "HEAD",
		})
		if result.code != 2 || !strings.Contains(result.stderr.String(), "requires LENGTH_GATE_BASE") {
			t.Fatalf("exit = %d, stderr %q, want missing base message", result.code, result.stderr.String())
		}
	})
	t.Run("non-pull-request events are skipped", func(t *testing.T) {
		result := mainRun(t, repository, config, map[string]string{"LENGTH_GATE_EVENT": "push"})
		if result.code != 0 || !strings.Contains(result.stdout.String(), "skipping event push") {
			t.Fatalf("exit = %d, stdout %q, want event skip", result.code, result.stdout.String())
		}
	})
}

// TestMainFlagsParse checks successful and rejected command-line flag parsing.
func TestMainFlagsParse(t *testing.T) {
	repository := trackedRepository(t, map[string]string{"src/file.ts": "clean\n"})
	config := writeTestConfig(t,
		`{"extensions":[],"exclude":[],"docs":[`+
			`{"name":"spec","include":[],"exclude":[],"target":1500,"markerAllowed":false},`+
			`{"name":"design record","include":[],"exclude":[],"target":2500,"markerAllowed":true}]}`,
	)
	t.Run("all and repeated skip-class flags parse", func(t *testing.T) {
		result := mainRun(t, repository, config, nil, "--all", "--skip-class", "spec", "--skip-class", "design record")
		if result.code != 0 || !strings.Contains(result.stdout.String(), "clean") {
			t.Fatalf("exit = %d, stdout %q, stderr %q, want clean", result.code, result.stdout.String(), result.stderr.String())
		}
	})
	t.Run("skip-class requires all", func(t *testing.T) {
		result := mainRun(t, repository, config, nil, "--skip-class", "spec")
		if result.code != 2 || !strings.Contains(result.stderr.String(), "--skip-class is allowed only with --all") {
			t.Fatalf("exit = %d, stderr %q, want skip-class usage error", result.code, result.stderr.String())
		}
	})
	t.Run("unknown skip class is rejected", func(t *testing.T) {
		result := mainRun(t, repository, config, nil, "--all", "--skip-class", "unknown")
		if result.code != 2 || !strings.Contains(result.stderr.String(), `unknown document class "unknown"`) {
			t.Fatalf("exit = %d, stderr %q, want unknown class error", result.code, result.stderr.String())
		}
	})
}
