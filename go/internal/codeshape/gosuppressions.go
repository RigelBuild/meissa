// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func goSuppressionFindings(ctx context.Context, root string, cfg Config) ([]Finding, error) {
	paths, err := trackedPaths(ctx, root)
	if err != nil {
		return nil, err
	}
	modules := goModules(paths, cfg)
	eligible := make(map[string]bool)
	for _, module := range modules {
		files, _, err := parseGoSources(root, module, paths, cfg)
		if err != nil {
			return nil, err
		}
		for path := range files {
			eligible[path] = true
		}
	}
	findings := []Finding{}
	for _, path := range paths {
		if !eligible[path] {
			continue
		}
		source, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // G304: path from the repo's tracked file list
		if err != nil {
			return nil, fmt.Errorf("read Go source %q: %w", path, err)
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, source, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse Go source %q: %w", path, err)
		}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if !strings.HasPrefix(comment.Text, "//") {
					continue
				}
				text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
				fields := strings.Fields(text)
				if len(fields) == 0 {
					continue
				}
				directive := fields[0]
				metrics := make(map[Metric]bool)
				if directive == "nolint" || directive == "nolint:all" {
					metrics[MetricGoCognitive] = true
					metrics[MetricGoLines] = true
				} else if after, ok := strings.CutPrefix(directive, "nolint:"); ok {
					for name := range strings.SplitSeq(after, ",") {
						switch name {
						case "all":
							metrics[MetricGoCognitive] = true
							metrics[MetricGoLines] = true
						case "gocognit", "gocyclo":
							metrics[MetricGoCognitive] = true
						case "funlen":
							metrics[MetricGoLines] = true
						}
					}
				}
				for metric := range metrics {
					line := set.Position(comment.Pos()).Line
					findings = append(findings, Finding{
						Path: path + ":" + strconv.Itoa(line), Metric: metric, Value: line, Limit: 0,
						Fix: "remove complexity nolint suppression",
					})
				}
			}
		}
	}
	sortFindings(findings)
	return findings, nil
}
