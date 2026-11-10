// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

// PolicyFindings reports TypeScript suppressions and policy drift.
func PolicyFindings(ctx context.Context, root string, cfg codeshape.Config) ([]codeshape.Finding, error) {
	paths, err := codeshape.TrackedPaths(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("collect TypeScript policy paths: %w", err)
	}
	findings := []codeshape.Finding{}
	hasTS := false
	for _, path := range paths {
		if !isTypeScriptPath(path) || !codeshape.CountedPath(path, cfg) {
			continue
		}
		hasTS = true
		source, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // G304: path from the repo's tracked file list
		if err != nil {
			return nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		findings = append(findings, tsDirectiveFindings(path, source)...)
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
		metric       codeshape.Metric
	}{
		{"noExcessiveCognitiveComplexity", "maxAllowedComplexity", codeshape.MetricTSCognitive},
		{"noExcessiveLinesPerFunction", "maxLines", codeshape.MetricTSLines},
	} {
		rule, _ := rules[check.name].(map[string]any)
		options, _ := rule["options"].(map[string]any)
		if value, ok := options[check.option]; ok && numericValue(value) != cfg.Functions.LimitFor(check.metric) {
			findings = append(findings, codeshape.Finding{
				Path: "biome.json", Metric: check.metric, Value: numericValue(value),
				Limit: cfg.Functions.LimitFor(check.metric), Fix: "match biome rule option to gate config",
			})
		}
	}
	return findings, nil
}

func tsDirectiveFindings(path string, source []byte) []codeshape.Finding {
	findings := []codeshape.Finding{}
	for i, line := range strings.Split(string(source), "\n") {
		pos := strings.Index(line, "biome-ignore")
		if pos < 0 {
			continue
		}
		directive, _, _ := strings.Cut(line[pos:], ":")
		for name, metric := range biomeRuleMetrics() {
			if strings.Contains(directive, strings.TrimPrefix(name, "lint/")) {
				findings = append(findings, codeshape.Finding{
					Path: path + ":" + strconv.Itoa(i+1), Metric: metric, Value: i + 1, Limit: 0,
					Fix: "remove biome-ignore suppression",
				})
			}
		}
	}
	return findings
}
