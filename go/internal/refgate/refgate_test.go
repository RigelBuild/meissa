// SPDX-License-Identifier: MIT OR Apache-2.0

//nolint:exhaustruct,lll,noinlineerr // Keep TS-derived cases direct and readable.
package refgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/samber/mo"
)

func testConfig() Config {
	return Config{
		Prefilter:        Prefilter{ERE: "acme", IgnoreCase: false},
		Patterns:         []Pattern{{Source: `\bacme\b`, Flags: ""}},
		Ignore:           []string{},
		CarveOutPaths:    []string{},
		CarveOutPrefixes: []string{},
		Allowlist:        map[string]string{},
		RemediationDoc:   mo.None[string](),
	}
}

func writeConfig(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gate.json")
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	t.Run("rejects an empty pattern list", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{}
		value := configJSON(config)
		path := writeConfig(t, value)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "non-empty") {
			t.Fatalf("LoadConfig error = %v, want non-empty pattern error", err)
		}
	})

	t.Run("rejects unknown keys at the top level and in prefilter", func(t *testing.T) {
		base := configJSON(testConfig())
		base["unexpected"] = true
		path := writeConfig(t, base)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), `Unknown key "unexpected"`) {
			t.Fatalf("top-level error = %v", err)
		}
		base = configJSON(testConfig())
		prefilter, ok := base["prefilter"].(map[string]any)
		if !ok {
			t.Fatal("config prefilter is not an object")
		}
		prefilter["extra"] = true
		path = writeConfig(t, base)
		_, err = LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), `Unknown key "extra"`) {
			t.Fatalf("prefilter error = %v", err)
		}
	})

	t.Run("rejects malformed JavaScript regular expressions", func(t *testing.T) {
		value := configJSON(testConfig())
		value["patterns"] = []any{map[string]any{"source": "(", "flags": ""}}
		_, err := LoadConfig(writeConfig(t, value))
		if err == nil || !strings.Contains(err.Error(), "Invalid regular expression") {
			t.Fatalf("LoadConfig error = %v", err)
		}
	})

	t.Run("rejects fields with the wrong type", func(t *testing.T) {
		value := configJSON(testConfig())
		prefilter, ok := value["prefilter"].(map[string]any)
		if !ok {
			t.Fatal("config prefilter is not an object")
		}
		prefilter["ignoreCase"] = "false"
		_, err := LoadConfig(writeConfig(t, value))
		if err == nil || !strings.Contains(err.Error(), "prefilter.ignoreCase must be a boolean") {
			t.Fatalf("LoadConfig error = %v", err)
		}
	})

	t.Run("maps every JavaScript flag", func(t *testing.T) {
		cases := []struct {
			name   string
			flags  string
			source string
			input  string
		}{
			{name: "i", flags: "i", source: "acme", input: "ACME"},
			{name: "m", flags: "m", source: "^acme", input: "before\nacme"},
			{name: "s", flags: "s", source: "a.*b", input: "a\nb"},
			{name: "u", flags: "u", source: `\u00e9`, input: "é"},
			{name: "g", flags: "g", source: "acme", input: "acme"},
			{name: "d", flags: "d", source: "acme", input: "acme"},
			{name: "y", flags: "y", source: "acme", input: "acme"},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				value := testConfig()
				value.Patterns = []Pattern{{Source: test.source, Flags: test.flags}}
				got, err := FindViolations(value, []string{"f.txt\x001\x00" + test.input})
				if err != nil || len(got) != 1 {
					t.Fatalf("FindViolations = %#v, %v; want one match", got, err)
				}
			})
		}
	})

	t.Run("rejects the v flag", func(t *testing.T) {
		value := configJSON(testConfig())
		value["patterns"] = []any{map[string]any{"source": "acme", "flags": "v"}}
		_, err := LoadConfig(writeConfig(t, value))
		if err == nil || !strings.Contains(err.Error(), "Invalid regular expression in patterns[0]:") {
			t.Fatalf("LoadConfig error = %v", err)
		}
	})
}

func configJSON(config Config) map[string]any {
	patterns := make([]any, 0, len(config.Patterns))
	for _, pattern := range config.Patterns {
		patterns = append(patterns, map[string]any{"source": pattern.Source, "flags": pattern.Flags})
	}
	value := map[string]any{
		"prefilter":        map[string]any{"ere": config.Prefilter.ERE, "ignoreCase": config.Prefilter.IgnoreCase},
		"patterns":         patterns,
		"ignore":           config.Ignore,
		"carveOutPaths":    config.CarveOutPaths,
		"carveOutPrefixes": config.CarveOutPrefixes,
		"allowlist":        config.Allowlist,
	}
	if doc, present := config.RemediationDoc.Get(); present {
		value["remediationDoc"] = doc
	}
	return value
}

func TestFindViolations(t *testing.T) {
	t.Run("throws on a hit it cannot parse instead of skipping it", func(t *testing.T) {
		_, err := FindViolations(testConfig(), []string{"no separators acme"})
		if err == nil || !strings.Contains(err.Error(), "unparsable grep hit") {
			t.Fatalf("FindViolations error = %v", err)
		}
	})

	t.Run("skips exact carve-outs and matching path prefixes", func(t *testing.T) {
		config := testConfig()
		config.CarveOutPaths = []string{"generated/one.txt"}
		config.CarveOutPrefixes = []string{"generated/docs/"}
		got, err := FindViolations(config, []string{
			"generated/one.txt:1:acme",
			"generated/docs/page.txt:2:acme",
			"docs/page.txt:3:acme",
		})
		want := []Reference{{File: "docs/page.txt", Line: 3, Text: "acme"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("FindViolations = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("skips an allowlisted exact path", func(t *testing.T) {
		config := testConfig()
		config.Allowlist["fixtures/example.txt"] = "Synthetic reference"
		got, err := FindViolations(config, []string{
			"fixtures/example.txt:1:acme",
			"docs/example.txt:2:acme",
		})
		want := []Reference{{File: "docs/example.txt", Line: 2, Text: "acme"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("FindViolations = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("removes the compound name before matching but keeps a bare name", func(t *testing.T) {
		config := testConfig()
		config.Ignore = []string{"acme-gate"}
		got, err := FindViolations(config, []string{
			"docs/compound.txt:1:acme-gate",
			"docs/bare.txt:2:acme-gate detects acme",
		})
		want := []Reference{{File: "docs/bare.txt", Line: 2, Text: "acme-gate detects acme"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("FindViolations = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("matches case-insensitive patterns and parses colons in paths and text", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: `\bacme\b`, Flags: "i"}}
		got, err := FindViolations(config, []string{"docs:notes/file.txt\x0042\x00ACME: see docs:notes again"})
		want := []Reference{{File: "docs:notes/file.txt", Line: 42, Text: "ACME: see docs:notes again"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("FindViolations = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("y-sticky matches only at index zero", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: "acme", Flags: "y"}}
		got, err := FindViolations(config, []string{"f.txt:1:acme first", "f.txt:2:before acme"})
		want := []Reference{{File: "f.txt", Line: 1, Text: "acme first"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("FindViolations = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("JSON string golden for U+2028", func(t *testing.T) {
		assertJSONGolden(t, "a\u2028b", `"a\u2028b"`)
	})
	t.Run("JSON string golden for U+2029", func(t *testing.T) {
		assertJSONGolden(t, "a\u2029b", `"a\u2029b"`)
	})
	t.Run("JSON string golden for invalid UTF-8", func(t *testing.T) {
		assertJSONGolden(t, string([]byte{'a', 0xff, 'b'}), `"a�b"`)
	})
	t.Run("JSON string golden for HTML characters", func(t *testing.T) {
		assertJSONGolden(t, "<>&", `"<>&"`)
	})
}

func assertJSONGolden(t *testing.T, value, want string) {
	t.Helper()
	got, err := jsonString(value)
	if err != nil || got != want {
		t.Fatalf("jsonString = %q, %v; want %q", got, err, want)
	}
}

func TestRunOnce(t *testing.T) {
	t.Run("returns 0 for a clean scan", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runOnce(t.Context(), "repo", testConfig(), func(_ context.Context, _ string, _ Prefilter) ([]string, error) {
			return []string{}, nil
		}, &stdout, &stderr)
		if code != 0 || stdout.String() != "ref-gate: clean\n" || stderr.Len() != 0 {
			t.Fatalf("runOnce = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("returns 1 and prints each violation plus the remediation hint", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: `\bacme\b`, Flags: "i"}}
		config.RemediationDoc = mo.Some("docs/reference-policy.md")
		var stdout, stderr bytes.Buffer
		code := runOnce(t.Context(), "repo", config, func(_ context.Context, _ string, _ Prefilter) ([]string, error) {
			return []string{"docs/one.md:7:acme", "docs/two.md:8:ACME"}, nil
		}, &stdout, &stderr)
		want := "docs/one.md:7: acme\ndocs/two.md:8: ACME\nSee docs/reference-policy.md.\n"
		if code != 1 || stderr.String() != want || stdout.Len() != 0 {
			t.Fatalf("runOnce = %d, stdout %q, stderr %q; want %q", code, stdout.String(), stderr.String(), want)
		}
	})

	t.Run("returns 2 when the scan fails", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runOnce(t.Context(), "repo", testConfig(), func(_ context.Context, _ string, _ Prefilter) ([]string, error) {
			return nil, errors.New("not a git tree") //nolint:err113 // This is the upstream TS test's synthetic error.
		}, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "not a git tree") {
			t.Fatalf("runOnce = %d, stderr %q", code, stderr.String())
		}
	})

	t.Run("passes the POSIX ERE prefilter verbatim instead of deriving it", func(t *testing.T) {
		config := testConfig()
		config.Prefilter = Prefilter{ERE: `\bacme\b`, IgnoreCase: true}
		var got Prefilter
		var stdout, stderr bytes.Buffer
		code := runOnce(t.Context(), "repo", config, func(_ context.Context, _ string, actual Prefilter) ([]string, error) {
			got = actual
			return []string{}, nil
		}, &stdout, &stderr)
		if code != 0 || got != config.Prefilter {
			t.Fatalf("runOnce = %d, seen %#v; want %#v", code, got, config.Prefilter)
		}
	})

	t.Run("reports MatchTimeout with pattern and reference", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: `(a+)+$`, Flags: ""}}
		var stdout, stderr bytes.Buffer
		hits := []string{"timeout.txt:12:" + strings.Repeat("a", 5000) + "!"}
		code := runOnceWithLimitsForTest(t, config, hits, time.Nanosecond, 100000, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "ref-gate: patterns[0] timed out on timeout.txt:12") {
			t.Fatalf("runOnce = %d, stderr %q", code, stderr.String())
		}
	})

	t.Run("reports backtracking stack limit with pattern and reference", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: `(a+)+$`, Flags: ""}}
		var stdout, stderr bytes.Buffer
		hits := []string{"limit.txt:14:" + strings.Repeat("a", 500) + "!"}
		code := runOnceWithLimitsForTest(t, config, hits, time.Second, 1, &stdout, &stderr)
		want := "ref-gate: patterns[0] hit the backtracking limit on limit.txt:14"
		if code != 2 || !strings.Contains(stderr.String(), want) {
			t.Fatalf("runOnce = %d, stderr %q", code, stderr.String())
		}
	})
}

func runOnceWithLimitsForTest(t *testing.T, config Config, hits []string, timeout time.Duration, stackLimit int, stdout, stderr *bytes.Buffer) int {
	t.Helper()
	grep := func(_ context.Context, _ string, _ Prefilter) ([]string, error) { return hits, nil }
	return runOnceWithLimits(t.Context(), "repo", config, grep, stdout, stderr, timeout, stackLimit)
}

func TestCLIUsage(t *testing.T) {
	t.Run("returns usage exit code when the config flag is missing", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Main(t.Context(), []string{}, func(string) string { return "" }, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "Usage: ref-gate --config <path>") {
			t.Fatalf("Main = %d, stderr %q", code, stderr.String())
		}
	})

	t.Run("returns internal-error exit code for an invalid config", func(t *testing.T) {
		value := configJSON(testConfig())
		value["unexpected"] = true
		var stdout, stderr bytes.Buffer
		args := []string{"--config", writeConfig(t, value)}
		code := Main(t.Context(), args, func(string) string { return "" }, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "Unknown key") {
			t.Fatalf("Main = %d, stderr %q", code, stderr.String())
		}
	})
}

func createTrackedRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", append([]string{"-C", repository}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	git("init", "-q")
	for name, text := range files {
		path := filepath.Join(repository, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", name)
	}
	return repository
}

func TestDecodeGitGrepOutput(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "isolated invalid byte",
			input: []byte{0xff},
			want:  "\uFFFD",
		},
		{
			name:  "preserves valid multibyte characters",
			input: []byte("café 😀"),
			want:  "café 😀",
		},
		{
			name:  "adjacent invalid bytes each get a replacement",
			input: []byte{0xff, 0xfe},
			want:  "\uFFFD\uFFFD",
		},
		{
			name:  "invalid continuation reprocesses the following bytes",
			input: []byte{0xe2, 0x28, 0xa1},
			want:  "\uFFFD(\uFFFD",
		},
		{
			name:  "truncated three-byte sequence",
			input: []byte{0xe2, 0x82},
			want:  "\uFFFD",
		},
		{
			name:  "truncated four-byte sequence",
			input: []byte{0xf0, 0x9f, 0x92},
			want:  "\uFFFD",
		},
		{
			name:  "invalid two-byte leading value",
			input: []byte{0xc0, 0x80},
			want:  "\uFFFD\uFFFD",
		},
		{
			name:  "overlong sequence",
			input: []byte{0xe0, 0x80, 0x80},
			want:  "\uFFFD\uFFFD\uFFFD",
		},
		{
			name:  "encoded surrogate",
			input: []byte{0xed, 0xa0, 0x80},
			want:  "\uFFFD\uFFFD\uFFFD",
		},
		{
			name:  "code point above Unicode maximum",
			input: []byte{0xf4, 0x90, 0x80, 0x80},
			want:  "\uFFFD\uFFFD\uFFFD\uFFFD",
		},
		{
			name:  "truncated sequence before invalid continuation",
			input: []byte{0xe2, 0x82, 0x28},
			want:  "\uFFFD(",
		},
		{
			name:  "leading byte order mark is omitted",
			input: []byte{0xef, 0xbb, 0xbf, 'a'},
			want:  "a",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decodeGitGrepOutput(test.input); got != test.want {
				t.Fatalf("decodeGitGrepOutput(% x) = %q; want %q", test.input, got, test.want)
			}
		})
	}
}

func TestGitGrep(t *testing.T) {
	t.Run("passes the POSIX ERE and case option to git grep", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{"fixture.txt": "Acme reference\nclean\n"})
		got, err := GitGrep(t.Context(), repository, Prefilter{ERE: "acme", IgnoreCase: true})
		want := []string{"fixture.txt\x001\x00Acme reference"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("GitGrep = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("returns no hits when git grep finds no match", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{"fixture.txt": "clean\n"})
		got, err := GitGrep(t.Context(), repository, Prefilter{ERE: "acme", IgnoreCase: false})
		if err != nil || len(got) != 0 {
			t.Fatalf("GitGrep = %#v, %v; want no hits", got, err)
		}
	})

	t.Run("throws on a git grep error instead of passing", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{"fixture.txt": "acme\n"})
		_, err := GitGrep(t.Context(), repository, Prefilter{ERE: "acme(", IgnoreCase: false})
		if err == nil || !strings.Contains(err.Error(), "git grep exited") {
			t.Fatalf("GitGrep error = %v", err)
		}
		_, err = GitGrep(t.Context(), t.TempDir(), Prefilter{ERE: "acme", IgnoreCase: false})
		if err == nil || !strings.Contains(err.Error(), "not the top level of a git repository") {
			t.Fatalf("GitGrep non-repository error = %v", err)
		}
	})

	t.Run("a newline in a tracked path cannot spoof a carve-out", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{
			"sub\ngate.json": "acme here\n",
			"other.txt":      "acme there\n",
		})
		config := testConfig()
		config.CarveOutPaths = []string{"gate.json"}
		hits, err := GitGrep(t.Context(), repository, config.Prefilter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FindViolations(config, hits)
		if err != nil {
			t.Fatal(err)
		}
		files := make([]string, 0, len(got))
		for _, reference := range got {
			files = append(files, reference.File)
		}
		want := []string{"other.txt", "sub\ngate.json"}
		if !reflect.DeepEqual(files, want) {
			t.Fatalf("files = %#v; want %#v", files, want)
		}
	})
	t.Run("replaces invalid UTF-8 from tracked content", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{
			"fixture.txt": "prefix \xff\xe2\x82 end\n",
		})
		got, err := GitGrep(t.Context(), repository, Prefilter{ERE: "prefix", IgnoreCase: false})
		want := []string{"fixture.txt\x001\x00prefix \uFFFD\uFFFD end"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("GitGrep = %#v, %v; want %#v", got, err, want)
		}
	})
	t.Run("replaces invalid UTF-8 in tracked paths", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{
			"fixture\xff.txt": "prefix\n",
		})
		got, err := GitGrep(t.Context(), repository, Prefilter{ERE: "prefix", IgnoreCase: false})
		want := []string{"fixture\uFFFD.txt\x001\x00prefix"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("GitGrep = %#v, %v; want %#v", got, err, want)
		}
	})

	t.Run("scans files git would treat as binary", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{
			".gitattributes": "marked.md binary\n",
			"marked.md":      "acme marked\n",
			"nul.md":         "acme nul\n\x00\n",
		})
		config := testConfig()
		hits, err := GitGrep(t.Context(), repository, config.Prefilter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FindViolations(config, hits)
		if err != nil {
			t.Fatal(err)
		}
		files := make([]string, 0, len(got))
		for _, reference := range got {
			files = append(files, reference.File)
		}
		want := []string{"marked.md", "nul.md"}
		if !reflect.DeepEqual(files, want) {
			t.Fatalf("files = %#v; want %#v", files, want)
		}
	})

	t.Run("accepts a root reached through a symlink", func(t *testing.T) {
		repository := createTrackedRepository(t, map[string]string{"fixture.txt": "acme\n"})
		link := filepath.Join(t.TempDir(), "repo")
		if err := os.Symlink(repository, link); err != nil {
			t.Fatal(err)
		}
		got, err := GitGrep(t.Context(), link, Prefilter{ERE: "acme", IgnoreCase: false})
		if err != nil || len(got) != 1 {
			t.Fatalf("GitGrep = %#v, %v; want one hit", got, err)
		}
	})
}

func TestMainEndToEnd(t *testing.T) {
	t.Run("scans staged files in a temporary Git repository", func(t *testing.T) {
		workspace := t.TempDir()
		repository := filepath.Join(workspace, "repo")
		if err := os.Mkdir(repository, 0o755); err != nil {
			t.Fatal(err)
		}
		configPath := writeConfig(t, configJSON(testConfig()))
		git := func(args ...string) {
			t.Helper()
			command := exec.CommandContext(t.Context(), "git", append([]string{"-C", repository}, args...)...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, output)
			}
		}
		git("init", "-q")
		fixture := filepath.Join(repository, "fixture.txt")
		run := func() (int, string, string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			code := Main(t.Context(), []string{"--config", configPath}, func(key string) string {
				if key == "GATE_ROOT" {
					return repository
				}
				return ""
			}, &stdout, &stderr)
			return code, stdout.String(), stderr.String()
		}

		if err := os.WriteFile(fixture, []byte("reference to acme\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "fixture.txt")
		code, _, stderr := run()
		if code != 1 || !strings.Contains(stderr, "fixture.txt:1: reference to acme") {
			t.Fatalf("violating run = %d, %q", code, stderr)
		}

		if err := os.WriteFile(fixture, []byte("clean content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "fixture.txt")
		code, stdout, stderr := run()
		if code != 0 || stdout != "ref-gate: clean\n" || stderr != "" {
			t.Fatalf("clean run = %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		if err := os.WriteFile(fixture, []byte("reference \xff to acme\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "fixture.txt")
		code, _, stderr = run()
		if code != 1 || stderr != "fixture.txt:1: reference \uFFFD to acme\n" {
			t.Fatalf("malformed content run = %d, %q", code, stderr)
		}

		if err := os.WriteFile(fixture, []byte("clean content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "fixture.txt")
		invalidPath := filepath.Join(repository, "fixture\xff.txt")
		if err := os.WriteFile(invalidPath, []byte("reference to acme\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "fixture\xff.txt")
		code, _, stderr = run()
		if code != 1 || stderr != "fixture\uFFFD.txt:1: reference to acme\n" {
			t.Fatalf("malformed filename run = %d, %q", code, stderr)
		}
	})
}

func TestParseGitGrepOutput(t *testing.T) {
	t.Run("parses records and reports the UTF-16 offset", func(t *testing.T) {
		got, err := ParseGitGrepOutput("f\x001\x00acme\n")
		if err != nil || !reflect.DeepEqual(got, []string{"f\x001\x00acme"}) {
			t.Fatalf("ParseGitGrepOutput = %#v, %v", got, err)
		}
		_, err = ParseGitGrepOutput("😀\x00x")
		if err == nil || err.Error() != "unparsable git grep output at offset 0" {
			t.Fatalf("ParseGitGrepOutput error = %v", err)
		}
	})
}

func TestRegexp2FailureClassification(t *testing.T) {
	t.Run("identifies the backtracking stack limit", func(t *testing.T) {
		config := testConfig()
		config.Patterns = []Pattern{{Source: `(a+)+$`, Flags: ""}}
		_, err := findViolationsWithLimits(config, []string{"f.txt:1:" + strings.Repeat("a", 500) + "!"}, time.Second, 1)
		if !errors.Is(err, regexp2.ErrBacktrackingStackLimit) && !strings.Contains(fmt.Sprint(err), "hit the backtracking limit") {
			t.Fatalf("findViolationsWithLimits error = %v", err)
		}
	})
}

func TestRepositoryPrefilterKeepsEveryConfiguredPattern(t *testing.T) {
	t.Run("the prefilter keeps a hit for every configured pattern", func(t *testing.T) {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("runtime.Caller failed")
		}
		configPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "ref-gate.config.json")
		config, err := LoadConfig(configPath)
		if err != nil {
			t.Fatal(err)
		}

		name := strings.Join([]string{"or", "ion"}, "")
		former := strings.Join([]string{"seal", "ed"}, "")
		issue := strings.Join([]string{"SE", "A-12"}, "")
		samples := []string{
			"the " + name + " repo",
			"see " + former + "/apps",
			former + " tools/gate.ts",
			"the " + former + "-monorepo",
			former + "'s docs",
			"the " + former + " platform",
			"see " + issue,
		}
		if len(samples) != len(config.Patterns) {
			t.Fatalf("samples count = %d; want %d", len(samples), len(config.Patterns))
		}

		repository := createTrackedRepository(t, map[string]string{
			"fixture.txt": strings.Join(samples, "\n") + "\n",
		})
		hits, err := GitGrep(t.Context(), repository, config.Prefilter)
		if err != nil {
			t.Fatal(err)
		}
		violations, err := FindViolations(config, hits)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]Reference, 0, len(samples))
		for index, sample := range samples {
			want = append(want, Reference{File: "fixture.txt", Line: index + 1, Text: sample})
		}
		if !reflect.DeepEqual(violations, want) {
			t.Fatalf("FindViolations = %#v; want %#v", violations, want)
		}
	})
}
