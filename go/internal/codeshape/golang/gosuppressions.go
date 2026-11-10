// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

// SuppressionFindings reports Go complexity suppressions.
func SuppressionFindings(ctx context.Context, root string, cfg codeshape.Config) ([]codeshape.Finding, error) {
	paths, err := codeshape.TrackedPaths(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("collect Go suppression paths: %w", err)
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
	findings := []codeshape.Finding{}
	for _, path := range paths {
		if !eligible[path] {
			continue
		}
		source, err := codeshape.ReadRepositoryFile(root, path)
		if err != nil {
			return nil, fmt.Errorf("read Go source %q: %w", path, err)
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, source, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse Go source %q: %w", path, err)
		}
		findings = append(findings, goCommentFindings(path, file.Comments, set)...)
	}
	codeshape.SortFindings(findings)
	return findings, nil
}

func goCommentFindings(path string, groups []*ast.CommentGroup, set *token.FileSet) []codeshape.Finding {
	findings := []codeshape.Finding{}
	for _, group := range groups {
		for _, comment := range group.List {
			for metric := range goDirectiveMetrics(comment.Text) {
				line := set.Position(comment.Pos()).Line
				findings = append(findings, codeshape.Finding{
					Path: path + ":" + strconv.Itoa(line), Metric: metric, Value: line, Limit: 0,
					Fix: "remove complexity nolint suppression",
				})
			}
		}
	}
	return findings
}

func goDirectiveMetrics(comment string) map[codeshape.Metric]bool {
	metrics := make(map[codeshape.Metric]bool)
	if !strings.HasPrefix(comment, "//") {
		return metrics
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(comment, "//")))
	if len(fields) == 0 {
		return metrics
	}
	directive := fields[0]
	if directive == "nolint" || directive == "nolint:all" {
		metrics[codeshape.MetricGoCognitive] = true
		metrics[codeshape.MetricGoLines] = true
	} else if after, ok := strings.CutPrefix(directive, "nolint:"); ok {
		for name := range strings.SplitSeq(after, ",") {
			switch name {
			case "all":
				metrics[codeshape.MetricGoCognitive] = true
				metrics[codeshape.MetricGoLines] = true
			case "gocognit", "gocyclo":
				metrics[codeshape.MetricGoCognitive] = true
			case "funlen":
				metrics[codeshape.MetricGoLines] = true
			}
		}
	}
	return metrics
}
