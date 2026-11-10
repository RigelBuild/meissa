// SPDX-License-Identifier: MIT OR Apache-2.0

package repochecks

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/titanous/json5"
)

type json5Record = map[string]any

func readJSON5Record(t *testing.T, path string) json5Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	decodeErr := json5.Unmarshal(data, &value)
	if decodeErr != nil {
		t.Fatalf("decode JSON5 %s: %v", path, decodeErr)
	}
	return asRecord(t, value, path)
}

func asRecord(t *testing.T, value any, label string) json5Record {
	t.Helper()
	record, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s must be an object, got %T", label, value)
	}
	return record
}

func asArray(t *testing.T, value any, label string) []any {
	t.Helper()
	array, ok := value.([]any)
	if ok {
		return array
	}
	strings, ok := value.([]string)
	if ok {
		array := make([]any, len(strings))
		for i, value := range strings {
			array[i] = value
		}
		return array
	}
	t.Fatalf("%s must be an array, got %T", label, value)
	return nil
}

func stringValues(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return values, true
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func hasStringField(record json5Record, field, wanted string) bool {
	values, ok := stringValues(record[field])
	return ok && containsString(values, wanted)
}

func asStrings(t *testing.T, value any, label string) []string {
	t.Helper()
	values, ok := stringValues(value)
	if !ok {
		t.Fatalf("%s must be an array of strings, got %T", label, value)
	}
	return values
}

func equalJSON(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("got %s, want %s", gotJSON, wantJSON)
	}
}

func resolvePresets(t *testing.T, root string, config json5Record) json5Record {
	t.Helper()
	references := asStrings(t, config["extends"], "Renovate extends")
	resolved := json5Record{}
	expanded := make([]string, 0, len(references))
	for _, reference := range references {
		expanded = append(expanded, reference)
		if reference == "config:recommended" {
			continue
		}
		if reference != "github>RigelBuild/meissa//tools/renovate/preset.json5" {
			t.Fatalf("Unsupported Renovate preset: %s", reference)
		}
		preset := readJSON5Record(t, filepath.Join(root, "tools", "renovate", "preset.json5"))
		maps.Copy(resolved, preset)
		if nested, ok := stringValues(preset["extends"]); ok {
			expanded = append(expanded, nested...)
		}
	}
	for key, value := range config {
		if key != "extends" {
			resolved[key] = value
		}
	}
	resolved["extends"] = uniqueStrings(expanded)
	return resolved
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsString(values []string, wanted string) bool {
	return slices.Contains(values, wanted)
}
func findRule(t *testing.T, config json5Record, predicate func(json5Record) bool) json5Record {
	t.Helper()
	for _, value := range asArray(t, config["packageRules"], "packageRules") {
		rule := asRecord(t, value, "package rule")
		if predicate(rule) {
			return rule
		}
	}
	t.Fatal("matching package rule not found")
	return nil
}

func findManager(t *testing.T, config json5Record, predicate func(json5Record) bool) json5Record {
	t.Helper()
	for _, value := range asArray(t, config["customManagers"], "customManagers") {
		manager := asRecord(t, value, "custom manager")
		if predicate(manager) {
			return manager
		}
	}
	t.Fatal("matching custom manager not found")
	return nil
}

func managerFor(t *testing.T, managers []json5Record, name string) json5Record {
	t.Helper()
	for _, manager := range managers {
		if manager["depNameTemplate"] == name {
			return manager
		}
	}
	t.Fatalf("manager for %s not found", name)
	return nil
}

func matchPin(t *testing.T, pins []byte, manager json5Record) [][]string {
	t.Helper()
	pattern := asStrings(t, manager["matchStrings"], "manager matchStrings")[0]
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return compiled.FindAllStringSubmatch(string(pins), -1)
}

func namedNixBlock(text, name string) string {
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + ` = \{[\s\S]*?^\s*\};`)
	return pattern.FindString(text)
}

func nixField(t *testing.T, block, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + ` = "([^"]+)"`)
	match := pattern.FindStringSubmatch(block)
	if len(match) != 2 {
		t.Fatalf("missing %s in Nix pin block", name)
	}
	return match[1]
}

func satisfiesLessThanMajor(version, constraint string) bool {
	parts := strings.Split(version, ".")
	if len(parts) == 0 || !strings.HasPrefix(constraint, "<") {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	limit, err := strconv.Atoi(strings.TrimPrefix(constraint, "<"))
	return err == nil && major < limit
}
