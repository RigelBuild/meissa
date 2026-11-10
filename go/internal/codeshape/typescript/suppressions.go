// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func tsPolicyFindings(ctx context.Context, root string, cfg Config) ([]Finding, error) {
	paths, err := trackedPaths(ctx, root)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	hasTS := false
	for _, path := range paths {
		if !isTypeScriptPath(path) || !countedPath(path, cfg) {
			continue
		}
		hasTS = true
		source, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // G304: path from the repo's tracked file list
		if err != nil {
			return nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		for i, line := range strings.Split(string(source), "\n") {
			pos := strings.Index(line, "biome-ignore")
			if pos < 0 {
				continue
			}
			directive, _, _ := strings.Cut(line[pos:], ":")
			for name, metric := range biomeRuleMetrics() {
				if strings.Contains(directive, strings.TrimPrefix(name, "lint/")) {
					findings = append(findings, Finding{
						Path: path + ":" + strconv.Itoa(i+1), Metric: metric, Value: i + 1, Limit: 0,
						Fix: "remove biome-ignore suppression",
					})
				}
			}
		}
	}
	if !hasTS {
		return findings, nil
	}
	config, err := readBiomeConfig(root)
	if os.IsNotExist(err) {
		return findings, nil
	}
	if err != nil {
		return nil, err
	}
	rules := nestedMap(config, "linter", "rules", "complexity")
	for _, check := range []struct {
		name, option string
		metric       Metric
	}{
		{"noExcessiveCognitiveComplexity", "maxAllowedComplexity", MetricTSCognitive},
		{"noExcessiveLinesPerFunction", "maxLines", MetricTSLines},
	} {
		rule, _ := rules[check.name].(map[string]any)
		options, _ := rule["options"].(map[string]any)
		if value, ok := options[check.option]; ok && numericValue(value) != cfg.Functions.limitFor(check.metric) {
			findings = append(findings, Finding{
				Path: "biome.json", Metric: check.metric, Value: numericValue(value),
				Limit: cfg.Functions.limitFor(check.metric), Fix: "match biome rule option to gate config",
			})
		}
	}
	return findings, nil
}
