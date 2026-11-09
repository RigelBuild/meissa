// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("valid CUE configuration and allowlist", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		config, allowlist, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if config.FileLimit != 500 || config.DirLimit != 10 || !allowlist.Present {
			t.Fatalf("Load = %#v, %#v", config, allowlist)
		}
	})
	t.Run("invalid config fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\"]\nexclude: [\"[\"]\n", "files: {}\n")
		if _, _, err := Load(root); err == nil {
			t.Fatal("Load accepted an invalid config")
		}
	})
	t.Run("missing reason fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 501}\n")
		if _, _, err := Load(root); err == nil {
			t.Fatal("Load accepted an entry without a reason")
		}
	})
	t.Run("short reason fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 501, reason: \"split files\"}\n")
		if _, _, err := Load(root); err == nil {
			t.Fatal("Load accepted a reason with fewer than three words")
		}
	})
}

func TestLoadMissingFiles(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	if err := os.Remove(filepath.Join(root, "tools", "code-shape-gate", "allowlist.cue")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "allowlist.cue") {
		t.Fatalf("Load error = %v, want missing allowlist", err)
	}
}

func TestMainModes(t *testing.T) {
	t.Run("write refuses to add without seed", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "needs --seed") {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("seed writes raised rollout entries", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", "--seed", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		entry, exists := allowlist.Files["large.go"]
		if !exists || entry.Value != 501 || entry.Reason != "seeded at rollout, burn down" {
			t.Fatalf("seeded entry = %#v, exists=%v", entry, exists)
		}
	})
}

func TestMainWriteUpdates(t *testing.T) {
	t.Run("write lowers entries", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 502, reason: \"split by concern\"}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowlist.Files["large.go"].Value; got != 501 {
			t.Fatalf("lowered value = %d, want 501", got)
		}
	})
	t.Run("write removes entries at limit", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"small.go\": {value: 501, reason: \"split by concern\"}\n")
		trackedFile(t, root, "small.go", strings.Repeat("line\n", 500))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", root}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		_, allowlist, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := allowlist.Files["small.go"]; exists {
			t.Fatal("--write retained an entry now within the limit")
		}
	})
	t.Run("base rejects a seeded added entry", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: {}\ndirs: {}\n")
		trackedFile(t, root, "large.go", strings.Repeat("line\n", 501))
		commitFiles(t, root)
		var stdout, stderr strings.Builder
		code := Main(t.Context(), []string{"--write", "--seed", root}, func(key string) string {
			if key == "GATE_BASE" {
				return "HEAD"
			}
			return ""
		}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stdout.String(), "do not add or raise") {
			t.Fatalf("Main = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

}

const validConfig = "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\", \".cue\"]\nexclude: []\n"

func writeConfigFiles(t *testing.T, root, config, allowlist string) {
	t.Helper()
	dir := filepath.Join(root, "tools", "code-shape-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"config.cue": config, "allowlist.cue": allowlist} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
