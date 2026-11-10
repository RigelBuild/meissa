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

func TestLoadFunctionEntries(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		valid        bool
	}{{"valid", "split by concern", true}, {"short", "two words", false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := newFixtureRepo(t)
			allow := `functions: "go-lines": "f.go": [{id: "work", value: 70, reason: "` + tc.reason + `"}]`
			writeConfigFiles(t, root, validConfig, allow)
			cfg, entries, err := Load(root)
			if (err == nil) != tc.valid {
				t.Fatalf("Load error = %v", err)
			}
			if tc.valid && (cfg.Functions.Go.Lines != 60 || entries.Functions[MetricGoLines]["f.go"][0].ID != "work") {
				t.Fatalf("decoded config=%#v entries=%#v", cfg, entries)
			}
		})
	}
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

const validConfig = "fileLimit: 500\ndirLimit: 10\nextensions: [\".go\", \".cue\"]\nexclude: []\n" +
	"functions: {go: {cognitive: 20, lines: 60, statements: 40}, ts: {cognitive: 15, lines: 50}}\n"

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
