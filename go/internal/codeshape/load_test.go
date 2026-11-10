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
		_, _, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("invalid config fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\"]\nexclude: [\"[\"]\n", "files: {}\n")
		_, _, err := Load(root)
		if err == nil {
			t.Fatal("Load accepted an invalid config")
		}
	})
	t.Run("missing reason fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 501}\n")
		_, _, err := Load(root)
		if err == nil {
			t.Fatal("Load accepted an entry without a reason")
		}
	})
	t.Run("short reason fails", func(t *testing.T) {
		root := newFixtureRepo(t)
		writeConfigFiles(t, root, validConfig, "files: \"large.go\": {value: 501, reason: \"split files\"}\n")
		_, _, err := Load(root)
		if err == nil {
			t.Fatal("Load accepted a reason with fewer than three words")
		}
	})
}

func TestLoadDirectoryLimits(t *testing.T) {
	for _, config := range []string{
		`dirLimits: "wide": {limit: 10, reason: "record decisions together"}`,
		`dirLimits: "wide": {limit: 0, reason: "record decisions together"}`,
		`dirLimits: "wide": {limit: 50, reason: "two words"}`,
	} {
		t.Run(config, func(t *testing.T) {
			root := newFixtureRepo(t)
			writeConfigFiles(t, root, validConfig+config+"\n", "files: {}\n")
			_, _, err := Load(root)
			if err == nil {
				t.Fatalf("Load accepted invalid config %s", config)
			}
		})
	}
}

func TestLoadRejectsImports(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, "import \"strings\"\n"+validConfig+"\nx: strings.ToUpper(\"x\")\n", "files: {}\n")
	_, _, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "imports are not permitted") {
		t.Fatalf("Load import error = %v", err)
	}
}

func TestLoadRejectsEscapingConfig(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	outside := filepath.Join(t.TempDir(), "config.cue")
	err := os.WriteFile(outside, []byte(validConfig), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tools/code-shape-gate/config.cue")
	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(outside, path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Load(root)
	if err == nil {
		t.Fatal("Load accepted a config symlink outside the repository")
	}
}

func TestLoadRootDirectoryEntry(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, `dirs: ".": {value: 11, reason: "split root files"}`)
	_, allow, err := Load(root)
	if err != nil || allow.Dirs[""].Value != 11 {
		t.Fatalf("root entry = %#v, error = %v", allow.Dirs, err)
	}
}

func TestLoadMissingFiles(t *testing.T) {
	root := newFixtureRepo(t)
	writeConfigFiles(t, root, validConfig, "files: {}\n")
	err := os.Remove(filepath.Join(root, "tools", "code-shape-gate", "allowlist.cue"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Load(root)
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
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"config.cue": config, "allowlist.cue": allowlist} {
		err = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}
}
