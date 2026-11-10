// SPDX-License-Identifier: MIT OR Apache-2.0

// Package golang collects per-function Go metrics and suppression findings.
package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
)

// SuppressionFindings finds Go lint suppressions on included Go source.
func SuppressionFindings(paths []string, cfg model.Config, repository *os.Root) ([]model.Finding, error) {
	var findings []model.Finding
	for _, path := range paths {
		if !goSourceIncluded(path, cfg) {
			continue
		}
		source, err := repository.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Go source %q: %w", path, err)
		}
		file, set, err := parseGoFile(path, source)
		if err != nil {
			return nil, err
		}
		findings = append(findings, suppressionFindings(path, file, set)...)
	}
	return findings, nil
}

func goSourceIncluded(path string, cfg model.Config) bool {
	return strings.HasSuffix(path, ".go") && inventory.CountedPath(path, cfg)
}

func parseGoFile(path string, source []byte) (*ast.File, *token.FileSet, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, source, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Go source %q: %w", path, err)
	}
	return file, set, nil
}

func suppressionFindings(path string, file *ast.File, set *token.FileSet) []model.Finding {
	var findings []model.Finding
	for _, group := range file.Comments {
		for _, comment := range group.List {
			metrics := suppressedGoMetrics(comment.Text)
			line := set.Position(comment.Pos()).Line
			for _, metric := range metrics {
				findings = append(findings, model.Finding{Path: path + ":" + strconv.Itoa(line),
					Metric: metric, Value: 1, Fix: "remove complexity nolint suppression"})
			}
		}
	}
	return findings
}

func suppressedGoMetrics(text string) []model.Metric {
	if !strings.HasPrefix(text, "//") {
		return nil
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(text, "//")))
	if len(fields) == 0 {
		return nil
	}
	switch fields[0] {
	case "nolint", "nolint:all":
		return []model.Metric{model.MetricGoCognitive, model.MetricGoLines}
	}
	if names, ok := strings.CutPrefix(fields[0], "nolint:"); ok {
		return selectedGoMetrics(names)
	}
	return nil
}

func selectedGoMetrics(names string) []model.Metric {
	complexity, lines := false, false
	for name := range strings.SplitSeq(names, ",") {
		switch name {
		case "all":
			complexity, lines = true, true
		case "gocognit", "gocyclo":
			complexity = true
		case "funlen":
			lines = true
		}
	}
	metrics := make([]model.Metric, 0, 2)
	if complexity {
		metrics = append(metrics, model.MetricGoCognitive)
	}
	if lines {
		metrics = append(metrics, model.MetricGoLines)
	}
	return metrics
}
