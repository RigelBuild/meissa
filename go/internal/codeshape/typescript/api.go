// SPDX-License-Identifier: MIT OR Apache-2.0

// Package typescript collects Biome function metrics and suppression findings.
package typescript

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
    "github.com/RigelBuild/meissa/go/internal/codeshape/model"
    "github.com/titanous/json5"
)
CUT 168*=219
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/titanous/json5"
)

// SuppressionFindings reports Biome suppression directives and policy drift.
func SuppressionFindings(paths []string, cfg model.Config, repository *os.Root) ([]model.Finding, error) {
	var findings []model.Finding
	hasTS := false
	for _, path := range paths {
		if !isTypeScriptPath(path) || !inventory.CountedPath(path, cfg) {
			continue
		}
		hasTS = true
		source, err := repository.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		findings = append(findings, suppressionLineFindings(path, source)...)
	}
	if !hasTS {
		return findings, nil
	}
	configs, err := biomeConfigs(paths, repository)
	if err != nil {
		return nil, err
	}
	for path, config := range configs {
		findings = append(findings, driftFindings(path, config, cfg)...)
	}
	return findings, nil
}

// ParseSuppressionsForTest parses Biome suppression directives from one source file.
func ParseSuppressionsForTest(path string, source []byte) []model.Finding {
    var findings []model.Finding
    for lineNumber, line := range strings.Split(string(source), "\n") {
        start := strings.Index(line, "biome-ignore")
        if start < 0 {
            continue
        }
        directive, _, _ := strings.Cut(line[start:], ":")
        if !isBiomeComplexitySuppression(directive) {
            continue
        }
        for _, metric := range []model.Metric{model.MetricTSCognitive, model.MetricTSLines} {
            findings = append(findings, model.Finding{Path: path + ":" + strconv.Itoa(lineNumber+1), Metric: metric, Value: 1})
        }
    }
    return findings
}
func isBiomeComplexitySuppression(directive string) bool {
	fields := strings.Fields(directive)
	if len(fields) < 2 || (fields[0] != "biome-ignore" && fields[0] != "biome-ignore-all" && fields[0] != "biome-ignore-start") {
		return false
	}
	for _, category := range strings.Split(strings.Join(fields[1:], " "), ",") {
		switch strings.TrimSpace(category) {
		case "lint", "lint/complexity", "lint/complexity/noExcessiveCognitiveComplexity", "lint/complexity/noExcessiveLinesPerFunction":
			return true
		}
	}
	return false
}

func biomeConfigs(paths []string, repository *os.Root) (map[string]map[string]any, error) {
	configs := make(map[string]map[string]any)
	for _, path := range paths {
		if filepath.Base(path) != "biome.json" && filepath.Base(path) != "biome.jsonc" {
			continue
		}
		config, err := readBiomeConfig(repository, path)
		if err != nil {
			return nil, err
		}
		configs[path] = config
	}
	return configs, nil
}

func readBiomeConfig(repository *os.Root, path string) (map[string]any, error) {
	data, err := repository.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var config map[string]any
	if err := json5.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return config, nil
}

func driftFindings(path string, config map[string]any, cfg model.Config) []model.Finding {
	var findings []model.Finding
	for _, override := range biomeComplexityRules(config) {
		for _, check := range []struct {
			name, option string
			metric       model.Metric
		}{
			{"noExcessiveCognitiveComplexity", "maxAllowedComplexity", model.MetricTSCognitive},
			{"noExcessiveLinesPerFunction", "maxLines", model.MetricTSLines},
		} {
			rule, exists := override[check.name].(map[string]any)
			option, optionExists := ruleOption(rule, check.option)
			if !exists || !optionExists || numericValue(option) != cfg.Functions.LimitFor(check.metric) || ruleLevel(rule) != "off" {
				value := numericValue(option)
				findings = append(findings, model.Finding{Path: path, Metric: check.metric,
					Value: value, Limit: cfg.Functions.LimitFor(check.metric), Fix: "match biome rule option to gate config"})
			}
		}
	}
	return findings
}

func biomeComplexityRules(config map[string]any) []map[string]any {
	rules := []map[string]any{nestedMap(config, "linter", "rules", "complexity")}
	if overrides, ok := config["overrides"].([]any); ok {
		for _, value := range overrides {
			if override, ok := value.(map[string]any); ok {
				rules = append(rules, nestedMap(override, "linter", "rules", "complexity"))
			}
		}
	}
	return rules
}

func ruleOption(rule map[string]any, key string) (any, bool) {
	options, ok := rule["options"].(map[string]any)
	if !ok {
		return nil, false
	}
	value, exists := options[key]
	return value, exists
}

func ruleLevel(rule map[string]any) string {
	level, ok := rule["level"].(string)
	if !ok {
		return ""
	}
	return level
}

func nestedMap(value map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		nested, ok := value[key].(map[string]any)
		if !ok {
			return nil
		}
		value = nested
	}
	return value
}

func numericValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}

