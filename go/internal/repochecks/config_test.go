// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

func TestRenovatePolicy(t *testing.T) {
	root := filepath.Dir(moduleRoot(t))
	paths := renovatePolicyPaths{
		root:   root,
		bot:    filepath.Join(root, "tools", "renovate", "bot-config.json5"),
		config: filepath.Join(root, "tools", "renovate", "config.json5"),
		preset: filepath.Join(root, "tools", "renovate", "preset.json5"),
		lock:   filepath.Join(root, "devenv.lock"),
	}
	t.Run("bot scope, onboarding, and relock commands are pinned", func(t *testing.T) {
		testBotPolicy(t, paths.bot)
	})
	t.Run("the shared preset holds cooldown policy while meissa keeps its local managers", func(t *testing.T) {
		testSharedPresetPolicy(t, paths.config, paths.preset)
	})
	t.Run("the resolved preset preserves the original shared policy", func(t *testing.T) {
		testResolvedPresetPolicy(t, paths.root, paths.config)
	})
	t.Run("the cooldown exemptions match bunfig minimumReleaseAgeExcludes", func(t *testing.T) {
		testCooldownExemptions(t, paths.root, paths.config)
	})
	t.Run("typescript stays on 6.x until 7.1 ships its API", func(t *testing.T) {
		testTypescriptVersionCap(t, paths.config)
	})
	t.Run("the nixpkgs lock manager has a same-branch relock with no cooldown", func(t *testing.T) {
		testNixpkgsRelockPolicy(t, paths.config)
	})
	t.Run("Go analyzer pins have Renovate managers and refresh their derived hashes", func(t *testing.T) {
		testGoAnalyzerPolicy(t, paths.root, paths.config)
	})
	t.Run("Go analyzer refresh config stays branch-scoped", func(t *testing.T) {
		testGoAnalyzerRefreshPolicy(t, readJSON5Record(t, paths.config))
	})
	t.Run("the seed lock tracks the older rolling nixpkgs node without follows", func(t *testing.T) {
		testSeedLockPolicy(t, paths.root, paths.config, paths.lock)
	})
}

type renovatePolicyPaths struct {
	root   string
	bot    string
	config string
	preset string
	lock   string
}

func testBotPolicy(t *testing.T, botPath string) {
	t.Helper()
	bot := readJSON5Record(t, botPath)
	equalJSON(t, bot["repositories"], []any{"RigelBuild/meissa"})
	if bot["onboarding"] != false {
		t.Fatalf("onboarding = %v, want false", bot["onboarding"])
	}
	equalJSON(t, bot["allowedCommands"], []any{
		"^devenv update nixpkgs$",
		"^nix flake update nixpkgs$",
		"^go -C go run \\.\\/cmd/refresh-nix-hashes$",
	})
}

func testSharedPresetPolicy(t *testing.T, configPath, presetPath string) {
	t.Helper()
	config := readJSON5Record(t, configPath)
	preset := readJSON5Record(t, presetPath)
	equalJSON(t, config["extends"], []any{"github>RigelBuild/meissa//tools/renovate/preset.json5"})
	for _, key := range []string{"schedule", "lockFileMaintenance", "minimumReleaseAge", "internalChecksFilter"} {
		if _, ok := config[key]; ok {
			t.Errorf("config unexpectedly defines %s", key)
		}
	}
	packageRules := asArray(t, config["packageRules"], "packageRules")
	for _, entry := range packageRules {
		if _, ok := asRecord(t, entry, "package rule")["schedule"]; ok {
			t.Errorf("package rule has a schedule: %#v", entry)
		}
	}
	equalJSON(t, config["enabledManagers"], []any{"custom.regex", "bun", "npm", "gomod"})
	equalJSON(t, config["postUpdateOptions"], []any{"gomodTidy"})
	if !containsString(asStrings(t, preset["extends"], "preset extends"), "config:recommended") {
		t.Fatal("shared preset lost config:recommended")
	}
	for key, want := range map[string]any{
		"dependencyDashboard":  true,
		"rebaseWhen":           "behind-base-branch",
		"minimumReleaseAge":    "5 days",
		"internalChecksFilter": "strict",
	} {
		if preset[key] != want {
			t.Errorf("preset %s = %v, want %v", key, preset[key], want)
		}
	}
	if _, ok := preset["enabledManagers"]; ok {
		t.Error("shared preset must not define enabledManagers")
	}
	if _, ok := preset["customManagers"]; ok {
		t.Error("shared preset must not define customManagers")
	}
}

func testResolvedPresetPolicy(t *testing.T, root, configPath string) {
	t.Helper()
	effective := resolvePresets(t, root, readJSON5Record(t, configPath))
	for key, want := range map[string]any{
		"minimumReleaseAge":    "5 days",
		"internalChecksFilter": "strict",
		"dependencyDashboard":  true,
		"rebaseWhen":           "behind-base-branch",
	} {
		if effective[key] != want {
			t.Errorf("effective %s = %v, want %v", key, effective[key], want)
		}
	}
	if !containsString(asStrings(t, effective["extends"], "extends"), "config:recommended") {
		t.Error("resolved extends lost config:recommended")
	}
	if !containsString(asStrings(t, effective["enabledManagers"], "enabledManagers"), "gomod") {
		t.Error("resolved managers lost gomod")
	}
}

func testCooldownExemptions(t *testing.T, root, configPath string) {
	t.Helper()
	config := readJSON5Record(t, configPath)
	bunfigData, err := os.ReadFile(filepath.Join(root, "bunfig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var bunfig map[string]any
	decodeErr := toml.Unmarshal(bunfigData, &bunfig)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	install := asRecord(t, bunfig["install"], "bunfig install")
	rules := asArray(t, config["packageRules"], "packageRules")
	exempt := make([]string, 0)
	for _, item := range rules {
		rule := asRecord(t, item, "package rule")
		minimumReleaseAge, hasCooldown := rule["minimumReleaseAge"]
		if hasCooldown && minimumReleaseAge == nil && hasStringField(rule, "matchPackageNames", "@types/bun") {
			exempt = append(exempt, "@types/bun", "bun-types")
		}
	}
	equalJSON(t, exempt, install["minimumReleaseAgeExcludes"])
}

func testTypescriptVersionCap(t *testing.T, configPath string) {
	t.Helper()
	config := readJSON5Record(t, configPath)
	typescriptRule := findRule(t, config, func(rule json5Record) bool {
		return hasStringField(rule, "matchPackageNames", "typescript")
	})
	rangeText, ok := typescriptRule["allowedVersions"].(string)
	if !ok {
		t.Fatalf("typescript allowedVersions = %T", typescriptRule["allowedVersions"])
	}
	if !satisfiesLessThanMajor("6.9.9", rangeText) || satisfiesLessThanMajor("7.0.2", rangeText) {
		t.Fatalf("typescript cap %q should admit 6.9.9 and reject 7.0.2", rangeText)
	}
}

func testNixpkgsRelockPolicy(t *testing.T, configPath string) {
	t.Helper()
	config := readJSON5Record(t, configPath)
	manager := findManager(t, config, func(manager json5Record) bool {
		return manager["depNameTemplate"] == "cachix/devenv-nixpkgs"
	})
	equalJSON(t, manager["managerFilePatterns"], []any{"/^devenv\\.lock$/"})
	equalJSON(t, manager["matchStrings"], []any{`"repo": "devenv-nixpkgs",\s*"rev": "(?<currentDigest>[a-f0-9]{40})"`})
	rule := findRule(t, config, func(rule json5Record) bool {
		return hasStringField(rule, "matchDepNames", "cachix/devenv-nixpkgs")
	})
	if _, exists := rule["schedule"]; exists {
		t.Errorf("relock rule has schedule: %v", rule["schedule"])
	}
	if value, exists := rule["minimumReleaseAge"]; !exists || value != nil {
		t.Errorf("relock minimumReleaseAge = %v, want null", value)
	}
	tasks := asRecord(t, rule["postUpgradeTasks"], "postUpgradeTasks")
	equalJSON(t, tasks["commands"], []any{"devenv update nixpkgs", "nix flake update nixpkgs"})
	equalJSON(t, tasks["fileFilters"], []any{"devenv.lock", "flake.lock"})
	if tasks["executionMode"] != "branch" {
		t.Errorf("executionMode = %v, want branch", tasks["executionMode"])
	}
}

func testGoAnalyzerPolicy(t *testing.T, root, configPath string) {
	t.Helper()
	config := readJSON5Record(t, configPath)
	pins, err := os.ReadFile(filepath.Join(root, "tools", "toolchain", "go-analysis-pins.nix"))
	if err != nil {
		t.Fatal(err)
	}
	analysis := make([]json5Record, 0)
	for _, value := range asArray(t, config["customManagers"], "customManagers") {
		manager := asRecord(t, value, "manager")
		if manager["depTypeTemplate"] == "go-analysis" {
			analysis = append(analysis, manager)
		}
	}
	if len(analysis) != 2 ||
		analysis[0]["depNameTemplate"] != "golangci/golangci-lint" ||
		analysis[1]["depNameTemplate"] != "uber-go/nilaway" {
		t.Fatalf("Go analysis managers = %#v", analysis)
	}
	for _, manager := range analysis {
		patterns := asStrings(t, manager["managerFilePatterns"], "managerFilePatterns")
		if !containsString(patterns, "/^tools/toolchain/go-analysis-pins\\.nix$/") {
			t.Errorf("manager file patterns = %#v", patterns)
		}
	}
	assertGoAnalyzerPinVersions(t, pins, analysis)
}

func assertGoAnalyzerPinVersions(t *testing.T, pins []byte, analysis []json5Record) {
	t.Helper()
	golangci := managerFor(t, analysis, "golangci/golangci-lint")
	nilaway := managerFor(t, analysis, "uber-go/nilaway")
	golangciMatches := matchPin(t, pins, golangci)
	nilawayMatches := matchPin(t, pins, nilaway)
	if len(golangciMatches) != 1 || len(nilawayMatches) != 1 {
		t.Fatalf("pin matches: golangci=%d nilaway=%d", len(golangciMatches), len(nilawayMatches))
	}
	golangciBlock := namedNixBlock(string(pins), "golangci-lint")
	nilawayBlock := namedNixBlock(string(pins), "nilaway")
	golangciTag := strings.TrimPrefix(nixField(t, golangciBlock, "tag"), "v")
	nilawayRevision := nixField(t, nilawayBlock, "rev")
	if golangciMatches[0][1] != golangciTag || nilawayMatches[0][1] != nilawayRevision {
		t.Fatal("manager values do not match current pin values")
}
}

func testGoAnalyzerRefreshPolicy(t *testing.T, config json5Record) {
	t.Helper()
	refresh := asRecord(t, config["postUpgradeTasks"], "postUpgradeTasks")
	nilawayRule := findRule(t, config, func(rule json5Record) bool {
		return hasStringField(rule, "matchDepNames", "uber-go/nilaway")
	})
	if nilawayRule["dependencyDashboardApproval"] != true {
		t.Error("nilaway must require dashboard approval")
	}
	equalJSON(t, refresh["commands"], []any{"go -C go run ./cmd/refresh-nix-hashes"})
	equalJSON(t, refresh["fileFilters"], []any{"tools/toolchain/go-analysis-pins.nix", "go/package.nix"})
	if refresh["executionMode"] != "branch" {
		t.Errorf("refresh executionMode = %v, want branch", refresh["executionMode"])
	}
	equalJSON(t, config["enabledManagers"], []any{"custom.regex", "bun", "npm", "gomod"})
	equalJSON(t, config["postUpdateOptions"], []any{"gomodTidy"})
	gotreesitter := findRule(t, config, func(rule json5Record) bool {
		return hasStringField(rule, "matchPackageNames", "github.com/odvcencio/gotreesitter")
	})
	if gotreesitter["automerge"] != false {
		t.Error("gotreesitter package rule must disable automerge")
	}
}

func testSeedLockPolicy(t *testing.T, root, configPath, lockPath string) {
	t.Helper()
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var lock json5Record
	err = json.Unmarshal(data, &lock)
	if err != nil {
		t.Fatal(err)
	}
	nodes := asRecord(t, lock["nodes"], "devenv.lock nodes")
	nixpkgs := asRecord(t, nodes["nixpkgs"], "nixpkgs lock node")
	if _, ok := nixpkgs["follows"]; ok {
		t.Fatal("nixpkgs lock node has follows")
	}
	locked := asRecord(t, nixpkgs["locked"], "nixpkgs locked")
	devenvYAML, err := os.ReadFile(filepath.Join(root, "devenv.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var devenv json5Record
	decodeErr := yaml.Unmarshal(devenvYAML, &devenv)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	inputs := asRecord(t, devenv["inputs"], "devenv.yaml inputs")
	nixpkgsInput := asRecord(t, inputs["nixpkgs"], "devenv.yaml inputs nixpkgs")
	if nixpkgsInput["url"] != "github:cachix/devenv-nixpkgs/rolling" {
		t.Errorf("nixpkgs URL = %v", nixpkgsInput["url"])
	}
	if _, ok := nixpkgsInput["follows"]; ok {
		t.Error("devenv.yaml nixpkgs input has follows")
	}
	config := readJSON5Record(t, configPath)
	manager := findManager(t, config, func(manager json5Record) bool {
		return manager["depNameTemplate"] == "cachix/devenv-nixpkgs"
	})
	pattern := regexp.MustCompile(asStrings(t, manager["matchStrings"], "matchStrings")[0])
	match := pattern.FindStringSubmatch(string(data))
	if len(match) < 2 || match[1] != locked["rev"] {
		t.Fatalf("nixpkgs lock manager revision = %v, locked rev = %v", match, locked["rev"])
	}
}
