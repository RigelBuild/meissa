// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// consumerFixture is a git repository that holds a copy of the biome-config package under node_modules.
type consumerFixture struct {
	directory        string
	packageDirectory string
	configPath       string
	fixturePath      string
}

func newConsumerFixture(t *testing.T) consumerFixture {
	t.Helper()
	directory := t.TempDir()
	// The preset enables VCS ignore files, so a consumer fixture must be a git repo.
	initResult := runProcess(t, "", nil, "git", "init", "-q", directory)
	if initResult.status != 0 {
		t.Fatalf("git init: %s", initResult.stderr)
	}
	writeFixtureFile(t, filepath.Join(directory, ".gitignore"), "node_modules/\ngenerated/\n")

	packageDirectory := filepath.Join(directory, "node_modules", "@rigelbuild", "biome-config")
	err := os.MkdirAll(packageDirectory, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(moduleRoot(t)), "packages", "biome-config")
	for _, name := range []string{"package.json", "biome.json"} {
		content := readFixtureFile(t, filepath.Join(source, name))
		writeFixtureFile(t, filepath.Join(packageDirectory, name), content)
	}
	configPath := filepath.Join(directory, "biome.json")
	writeFixtureFile(t, configPath,
		"{\n\t\"root\": true,\n\t\"extends\": [\"./space.json\", \"@rigelbuild/biome-config/biome\"]\n}\n")
	writeFixtureFile(t, filepath.Join(directory, "space.json"),
		"{\n\t\"root\": false,\n\t\"formatter\": { \"indentStyle\": \"space\" }\n}\n")
	return consumerFixture{
		directory:        directory,
		packageDirectory: packageDirectory,
		configPath:       configPath,
		fixturePath:      filepath.Join(directory, "fixture.ts"),
	}
}

func biomeFormat(t *testing.T, f consumerFixture) processResult {
	t.Helper()
	return runProcess(t, f.directory, nil, "biome", "format", "--write", "--config-path", f.configPath, f.fixturePath)
}

func biomeCI(t *testing.T, f consumerFixture) processResult {
	t.Helper()
	return runProcess(t, f.directory, nil, "biome", "ci", "--config-path", f.configPath, ".")
}

// isBiomePreset reports whether value has the preset fields the tests read.
func isBiomePreset(value map[string]any) bool {
	formatter, ok := value["formatter"].(map[string]any)
	if !ok {
		return false
	}
	if _, ok := formatter["indentStyle"].(string); !ok {
		return false
	}
	linter, ok := value["linter"].(map[string]any)
	if !ok {
		return false
	}
	if _, ok := linter["enabled"].(bool); !ok {
		return false
	}
	rules, ok := linter["rules"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = rules["preset"].(string)
	return ok
}

func parseBiomePreset(t *testing.T, source string) map[string]any {
	t.Helper()
	var value map[string]any
	err := json.Unmarshal([]byte(source), &value)
	if err != nil {
		t.Fatalf("parse Biome preset: %v", err)
	}
	if !isBiomePreset(value) {
		t.Fatal("Invalid Biome preset fixture")
	}
	return value
}

func TestSharedConfigurationPackages(t *testing.T) {
	t.Run("Node resolves both exported config subpaths", func(t *testing.T) {
		root := filepath.Dir(moduleRoot(t))
		script := `console.log(import.meta.resolve("@rigelbuild/tsconfig/base.json")); ` +
			`console.log(import.meta.resolve("@rigelbuild/biome-config/biome"))`
		result := runProcess(t, root, nil, "node", "--input-type=module", "-e", script)
		if result.status != 0 {
			t.Fatalf("node exit = %d: %s", result.status, result.stderr)
		}
		lines := strings.Split(jstext.Trim(result.stdout), "\n")
		got := make([]string, 0, len(lines))
		for _, line := range lines {
			parsed, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, parsed.Path)
		}
		want := []string{
			filepath.Join(root, "packages", "tsconfig", "base.json"),
			filepath.Join(root, "packages", "biome-config", "biome.json"),
		}
		if !slices.Equal(got, want) {
			t.Fatalf("resolved %q, want %q", got, want)
		}
	})

	t.Run("Node rejects the Biome subpath when its export key is removed", func(t *testing.T) {
		f := newConsumerFixture(t)
		manifestPath := filepath.Join(f.packageDirectory, "package.json")
		originalManifest := readFixtureFile(t, manifestPath)
		manifestWithoutBiomeExport := strings.Replace(originalManifest,
			`"./biome": "./biome.json"`, `"./other": "./biome.json"`, 1)
		if manifestWithoutBiomeExport == originalManifest {
			t.Fatal("manifest has no ./biome export to remove")
		}
		writeFixtureFile(t, manifestPath, manifestWithoutBiomeExport)
		resolverPath := filepath.Join(f.directory, "resolve.mjs")
		writeFixtureFile(t, resolverPath, "import.meta.resolve(\"@rigelbuild/biome-config/biome\");\n")

		result := runProcess(t, f.directory, nil, "node", resolverPath)
		if result.status == 0 {
			t.Fatal("node resolved the removed ./biome export")
		}
		if !strings.Contains(result.stderr, "ERR_PACKAGE_PATH_NOT_EXPORTED") {
			t.Fatalf("stderr = %q, want ERR_PACKAGE_PATH_NOT_EXPORTED", result.stderr)
		}
	})

	t.Run("shared preset preserves tabs and converts spaces to tabs", func(t *testing.T) {
		f := newConsumerFixture(t)
		tabInput := "export const preset = {\n\tshared: true,\n};\n"
		writeFixtureFile(t, f.fixturePath, tabInput)
		tabResult := biomeFormat(t, f)
		if tabResult.status != 0 {
			t.Fatalf("biome format exit = %d: %s", tabResult.status, tabResult.stderr)
		}
		if got := readFixtureFile(t, f.fixturePath); got != tabInput {
			t.Fatalf("tab input formatted to %q, want %q", got, tabInput)
		}

		writeFixtureFile(t, f.fixturePath, "export const preset = {\n  shared: true,\n};\n")
		spaceResult := biomeFormat(t, f)
		if spaceResult.status != 0 {
			t.Fatalf("biome format exit = %d: %s", spaceResult.status, spaceResult.stderr)
		}
		if got := readFixtureFile(t, f.fixturePath); got != tabInput {
			t.Fatalf("space input formatted to %q, want %q", got, tabInput)
		}
	})

	t.Run("removing preset indentStyle restores the earlier space setting", func(t *testing.T) {
		f := newConsumerFixture(t)
		presetPath := filepath.Join(f.packageDirectory, "biome.json")
		preset := parseBiomePreset(t, readFixtureFile(t, presetPath))
		withoutFormatter := maps.Clone(preset)
		delete(withoutFormatter, "formatter")
		if reflect.DeepEqual(withoutFormatter, preset) {
			t.Fatal("removing the formatter left the preset unchanged")
		}
		tabInput := "export const preset = {\n\tshared: true,\n};\n"
		writeFixtureFile(t, f.fixturePath, tabInput)
		encoded, err := json.MarshalIndent(withoutFormatter, "", "\t")
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, presetPath, string(encoded)+"\n")

		result := biomeFormat(t, f)
		if result.status != 0 {
			t.Fatalf("biome format exit = %d: %s", result.status, result.stderr)
		}
		formatted := readFixtureFile(t, f.fixturePath)
		if formatted == tabInput {
			t.Fatal("formatting kept tabs after the preset indentStyle was removed")
		}
		if !strings.Contains(formatted, "  shared: true") {
			t.Fatalf("formatted = %q, want two-space indent", formatted)
		}
	})

	t.Run("Biome CI accepts a consumer fixture extending the shared preset", func(t *testing.T) {
		f := newConsumerFixture(t)
		writeFixtureFile(t, f.fixturePath, "export const preset = {\n\tshared: true,\n};\n")
		result := biomeCI(t, f)
		if result.status != 0 {
			t.Fatalf("biome ci exit = %d: %s%s", result.status, result.stdout, result.stderr)
		}
	})

	t.Run("a consumer extending the preset skips gitignored files", func(t *testing.T) {
		f := newConsumerFixture(t)
		writeFixtureFile(t, f.configPath,
			"{\n\t\"root\": true,\n\t\"extends\": [\"@rigelbuild/biome-config/biome\"]\n}\n")
		writeFixtureFile(t, f.fixturePath, "export const ok = true;\n")
		generated := filepath.Join(f.directory, "generated")
		err := os.Mkdir(generated, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(generated, "bad.ts"), "export   const bad   =  1\n")
		result := biomeCI(t, f)
		if result.status != 0 {
			t.Fatalf("biome ci exit = %d: %s%s", result.status, result.stdout, result.stderr)
		}
	})

	t.Run("Biome CI rejects debugger under recommended lint rules", func(t *testing.T) {
		f := newConsumerFixture(t)
		writeFixtureFile(t, f.fixturePath, "debugger;\n")
		if result := biomeCI(t, f); result.status == 0 {
			t.Fatal("biome ci accepted debugger")
		}
		writeFixtureFile(t, f.fixturePath, "const ready = true;\n")
		result := biomeCI(t, f)
		if result.status != 0 {
			t.Fatalf("biome ci exit = %d: %s%s", result.status, result.stdout, result.stderr)
		}
	})
}
