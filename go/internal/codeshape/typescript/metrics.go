// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/codeshape/inventory"
	"github.com/RigelBuild/meissa/go/internal/codeshape/model"
	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"
)

const maxBiomeDiagnostics = 1_000_000

var (
	errBiomeTruncated       = errors.New("biome truncated diagnostics")
	errBiomeUnknownSource   = errors.New("biome diagnostic references unknown source")
	errBiomeMissingOutput   = errors.New("parse biome output: missing summary or diagnostics")
	errBiomeReported        = errors.New("biome reported errors")
	errBiomeValue           = errors.New("cannot parse biome value")
	errBiomePosition        = errors.New("invalid biome source position")
	errBiomeOutsideSource   = errors.New("biome position outside source")
	errBiomeOutsideFunction = errors.New("biome position outside any function")
)

func biomeRuleMetrics() map[string]model.Metric {
	return map[string]model.Metric{
		"lint/complexity/noExcessiveCognitiveComplexity": model.MetricTSCognitive,
		"lint/complexity/noExcessiveLinesPerFunction":    model.MetricTSLines,
	}
}

// Metrics measures Biome's function complexity and line count rules.
func Metrics(ctx context.Context, root string, paths []string, cfg model.Config, repository *os.Root) (model.FunctionMeasurements, []model.Skip, error) {
	sources, sourcePaths, err := readTypeScriptSources(paths, cfg, repository)
	if err != nil || len(sources) == 0 {
		return model.NewFunctionMeasurements(), nil, err
	}
	binary, err := exec.LookPath("biome")
	if err != nil {
		return nil, nil, fmt.Errorf("find biome: %w", err)
	}
	report, err := runBiome(ctx, binary, root, sourcePaths)
	if err != nil {
		return nil, nil, err
	}
	if report.Summary.DiagnosticsNotPrinted != 0 {
		return nil, nil, fmt.Errorf("%w: %d were not printed", errBiomeTruncated, report.Summary.DiagnosticsNotPrinted)
	}
	skipped, err := skippedBiomeSources(ctx, binary, root, sourcePaths, report)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range skipped {
		delete(sources, path)
	}
	return collectTypeScriptMetrics(report, sources, skipped)
}

func readTypeScriptSources(paths []string, cfg model.Config, repository *os.Root) (map[string][]byte, []string, error) {
	sources := make(map[string][]byte)
	for _, path := range paths {
		if !isTypeScriptPath(path) || !inventory.CountedPath(path, cfg) {
			continue
		}
		source, err := repository.ReadFile(filepath.FromSlash(path))
		if err != nil {
			return nil, nil, fmt.Errorf("read TypeScript source %q: %w", path, err)
		}
		sources[path] = source
	}
	sourcePaths := make([]string, 0, len(sources))
	for path := range sources {
		sourcePaths = append(sourcePaths, path)
	}
	sort.Strings(sourcePaths)
	return sources, sourcePaths, nil
}

func skippedBiomeSources(ctx context.Context, binary, root string, paths []string, report biomeReport) ([]model.Skip, error) {
	if report.Summary.Changed+report.Summary.Unchanged == len(paths) {
		return nil, nil
	}
	var skipped []model.Skip
	for _, path := range paths {
		single, err := runBiome(ctx, binary, root, []string{path})
		if err != nil {
			return nil, err
		}
		if single.Summary.Changed+single.Summary.Unchanged == 0 {
			skipped = append(skipped, model.Skip{Path: path, Reason: "ignored by Biome"})
		}
	}
	return skipped, nil
}

func collectTypeScriptMetrics(report biomeReport, sources map[string][]byte, skips []model.Skip) (model.FunctionMeasurements, []model.Skip, error) {
	values := model.NewFunctionMeasurements()
	files := make(map[string][]tsFunction, len(sources))
	trees := make([]*gotreesitter.Tree, 0, len(sources))
	defer func() {
		for _, tree := range trees {
			tree.Release()
		}
	}()
	for path, source := range sources {
		functions, tree, err := parseTSFunctions(path, source)
		if err != nil {
			skips = append(skips, model.Skip{Path: path, Reason: err.Error()})
			delete(sources, path)
			continue
		}
		trees = append(trees, tree)
		files[path] = functions
		for _, function := range functions {
			if err := model.AppendFunctionValue(values, model.MetricTSCognitive, path, function.id, 0); err != nil {
				return nil, nil, err
			}
			if err := model.AppendFunctionValue(values, model.MetricTSLines, path, function.id, 0); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := applyBiomeDiagnostics(report, files, sources, values, skips); err != nil {
		return nil, nil, err
	}
	return values, skips, nil
}

func applyBiomeDiagnostics(report biomeReport, files map[string][]tsFunction, sources map[string][]byte,
	values model.FunctionMeasurements, skips []model.Skip) error {
	metrics := biomeRuleMetrics()
	for _, diagnostic := range report.Diagnostics {
		metric, relevant := metrics[diagnostic.Category]
		if !relevant {
			continue
		}
		path := filepath.ToSlash(diagnostic.Location.Path)
		functions, exists := files[path]
		if isSkippedTSPath(path, skips) {
			continue
		}
		if !exists {
			return fmt.Errorf("%w %q", errBiomeUnknownSource, path)
		}
		function, err := findTSFunction(functions, diagnostic.Location.Start.Line, diagnostic.Location.Start.Column, sources[path])
		if err != nil {
			return err
		}
		value, err := biomeValue(diagnostic.Message, metric)
		if err != nil {
			return err
		}
		if err := model.SetFunctionDiagnostic(values, metric, path, function.id, value); err != nil {
			return err
		}
	}
	return nil
}

func isSkippedTSPath(path string, skips []model.Skip) bool {
	for _, skip := range skips {
		if skip.Path == path {
			return true
		}
	}
	return false
}

func isTypeScriptPath(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func runBiome(ctx context.Context, binary, root string, paths []string) (biomeReport, error) {
	args := []string{"lint", "--reporter=json", "--max-diagnostics=" + strconv.Itoa(maxBiomeDiagnostics),
		"--only=complexity/noExcessiveCognitiveComplexity", "--only=complexity/noExcessiveLinesPerFunction", "--no-errors-on-unmatched"}
	args = append(args, paths...)
	//nolint:gosec // G204: execute the configured biome linter
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return biomeReport{}, fmt.Errorf("biome lint failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var report biomeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return biomeReport{}, fmt.Errorf("parse biome output: %w", err)
	}
	if report.Summary == nil || report.Diagnostics == nil {
		return biomeReport{}, errBiomeMissingOutput
	}
	if report.Summary.Errors > 0 {
		return biomeReport{}, fmt.Errorf("biome reported %d errors: %w", report.Summary.Errors, errBiomeReported)
	}
	return report, nil
}

func biomeValue(message string, metric model.Metric) (int, error) {
	fields := strings.Fields(message)
	for index, field := range fields {
		if index+1 < len(fields) && (metric == model.MetricTSCognitive && field == "of" || metric == model.MetricTSLines && field == "lines") {
			value, err := strconv.Atoi(strings.Trim(fields[index+1], ".()"))
			if err != nil {
				return 0, fmt.Errorf("parse biome value: %w", err)
			}
			return value, nil
		}
	}
	return 0, fmt.Errorf("cannot parse %s value from %q: %w", metric, message, errBiomeValue)
}

func parseTSFunctions(path string, source []byte) ([]tsFunction, *gotreesitter.Tree, error) {
	lang := typescript.Language()
	if filepath.Ext(path) == ".tsx" {
		lang = tsx.Language()
	}
	tree, err := gotreesitter.NewParser(lang).Parse(source)
	if err != nil {
		return nil, nil, fmt.Errorf("parse TypeScript %q: %w", path, err)
	}
	root := tree.RootNode()
	if root.HasErrorOrMissing() {
		tree.Release()
		return nil, nil, fmt.Errorf("parse TypeScript %q: syntax errors", path)
	}
	functions := make([]tsFunction, 0)
	collectTSFunctions(root, lang, nil, &functions, source)
	assignTSFunctionIDs(functions, lang, source)
	return functions, tree, nil
}

func assignTSFunctionIDs(functions []tsFunction, lang *gotreesitter.Language, source []byte) {
	counts := make(map[string]int)
	for index := range functions {
		parent := enclosingTSFunction(functions, index)
		class := enclosingTSClass(functions[index].node, lang, source)
		base := parent + class + functions[index].id
		counts[base]++
		functions[index].id = base
		if counts[base] > 1 {
			functions[index].id += fmt.Sprintf("#%d", counts[base])
		}
	}
}

func enclosingTSFunction(functions []tsFunction, index int) string {
	for candidate := index - 1; candidate >= 0; candidate-- {
		if functions[candidate].node.StartByte() <= functions[index].node.StartByte() &&
			functions[candidate].node.EndByte() >= functions[index].node.EndByte() {
			return functions[candidate].id + "/"
		}
	}
	return ""
}

func enclosingTSClass(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Type(lang) != "class_declaration" && parent.Type(lang) != "class" {
			continue
		}
		if name := parent.ChildByFieldName("name", lang); name != nil {
			return name.Text(source) + "/"
		}
	}
	return ""
}

func collectTSFunctions(node *gotreesitter.Node, lang *gotreesitter.Language, titlePath []string,
	functions *[]tsFunction, source []byte) {
	if node == nil {
		return
	}
	if isTestTitleCall(node, lang, source) {
		arguments := node.ChildByFieldName("arguments", lang)
		title, callback := firstStringArgument(arguments, lang, source), lastFunctionArgument(arguments, lang)
		if title != "" && callback != nil {
			collectTSFunctions(callback, lang, append(append([]string(nil), titlePath...), title), functions, source)
			return
		}
	}
	if isTSFunctionNode(node, lang) {
		*functions = append(*functions, tsFunction{node: node, id: tsFunctionID(node, lang, titlePath, source)})
	}
	for index := range node.ChildCount() {
		collectTSFunctions(node.Child(index), lang, titlePath, functions, source)
	}
}

func isTestTitleCall(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	if node.Type(lang) != "call_expression" {
		return false
	}
	callable := node.ChildByFieldName("function", lang)
	if callable == nil {
		return false
	}
	text := callable.Text(source)
	return text == "describe" || text == "test" || text == "it" ||
		strings.HasPrefix(text, "describe.") || strings.HasPrefix(text, "test.") || strings.HasPrefix(text, "it.")
}

func isTSFunctionNode(node *gotreesitter.Node, lang *gotreesitter.Language) bool {
	switch node.Type(lang) {
	case "function_declaration", "function_expression", "arrow_function", "method_definition",
		"generator_function_declaration", "generator_function":
		return true
	default:
		return false
	}
}

func firstStringArgument(arguments *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if arguments == nil || arguments.NamedChildCount() == 0 {
		return ""
	}
	child := arguments.NamedChild(0)
	if child.Type(lang) != "string" && (child.Type(lang) != "template_string" || strings.Contains(child.Text(source), "${")) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(child.Text(source)), "`\"'")
}

func lastFunctionArgument(arguments *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if arguments == nil {
		return nil
	}
	for index := arguments.NamedChildCount() - 1; index >= 0; index-- {
		child := arguments.NamedChild(index)
		if isTSFunctionNode(child, lang) {
			return child
		}
	}
	return nil
}

func tsFunctionID(node *gotreesitter.Node, lang *gotreesitter.Language, titlePath []string, source []byte) string {
	name := node.ChildByFieldName("name", lang)
	functionName := "anonymous"
	if name != nil {
		functionName = name.Text(source)
	} else if parent := node.Parent(); parent != nil {
		if property := parent.ChildByFieldName("name", lang); property != nil {
			functionName = property.Text(source)
		} else if key := parent.ChildByFieldName("key", lang); key != nil {
			functionName = key.Text(source)
		}
	}
	if len(titlePath) > 0 {
		functionName = strings.Join(titlePath, "/") + "/" + functionName
	}
	return functionName
}

func findTSFunction(functions []tsFunction, line, column int, source []byte) (*tsFunction, error) {
	offset, err := biomeOffset(source, line, column)
	if err != nil {
		return nil, err
	}
	var match *tsFunction
	var smallest uint32
	for index := range functions {
		function := &functions[index]
		if int64(function.node.StartByte()) <= int64(offset) && int64(offset) <= int64(function.node.EndByte()) {
			span := function.node.EndByte() - function.node.StartByte()
			if match == nil || span < smallest {
				match, smallest = function, span
			}
		}
	}
	if match == nil {
		return nil, fmt.Errorf("biome position %d:%d is outside any function: %w", line, column, errBiomeOutsideFunction)
	}
	return match, nil
}

func biomeOffset(source []byte, line, column int) (int, error) {
	if line < 1 || column < 1 {
		return 0, errBiomePosition
	}
	lineStart := 0
	for current := 1; current < line; current++ {
		next := bytes.IndexByte(source[lineStart:], '\n')
		if next < 0 {
			return 0, fmt.Errorf("biome position line %d is outside source: %w", line, errBiomeOutsideSource)
		}
		lineStart += next + 1
	}
	offset := lineStart
	for units := 1; units < column; {
		if offset >= len(source) || source[offset] == '\n' {
			return 0, fmt.Errorf("biome position %d:%d is outside source: %w", line, column, errBiomeOutsideSource)
		}
		r, size := utf8.DecodeRune(source[offset:])
		units++
		if r > 0xffff {
			units++
		}
		offset += size
	}
	return offset, nil
}
