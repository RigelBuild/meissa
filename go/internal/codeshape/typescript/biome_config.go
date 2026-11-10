// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func writeGateBiomeConfig(dir string, limits codeshape.FunctionLimits, parser json.RawMessage, maxSize int) (
	string, error,
) {
	config := map[string]any{
		"vcs":   map[string]any{"enabled": false},
		"files": map[string]any{"includes": []string{"**"}, "maxSize": maxSize},
		"linter": map[string]any{"rules": map[string]any{"recommended": false, "complexity": map[string]any{
			"noExcessiveLinesPerFunction": map[string]any{
				"level": "warn", "options": map[string]int{"maxLines": limits.TS.Lines}},
			"noExcessiveCognitiveComplexity": map[string]any{
				"level": "warn", "options": map[string]int{"maxAllowedComplexity": limits.TS.Cognitive}},
		}}},
	}
	if len(parser) > 0 {
		config["javascript"] = map[string]any{"parser": parser}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode Biome config: %w", err)
	}
	name := filepath.Join(dir, "biome.json")
	err = os.WriteFile(name, data, 0o600)
	if err != nil {
		return "", fmt.Errorf("write Biome config: %w", err)
	}
	return name, nil
}

func rootBiomeParser(root string) (json.RawMessage, error) {
	for _, name := range []string{"biome.json", "biome.jsonc", ".biome.json", ".biome.jsonc"} {
		config, err := readBiomeConfig(root, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		javascript, ok := config["javascript"].(map[string]any)
		if !ok || javascript["parser"] == nil {
			return nil, nil
		}
		data, err := json.Marshal(javascript["parser"])
		if err != nil {
			return nil, fmt.Errorf("encode Biome parser options: %w", err)
		}
		return data, nil
	}
	return nil, nil
}
func runBiome(ctx context.Context, binary, root, configDir string, paths []string) (biomeReport, error) {
	args := make([]string, 0, 5+len(paths))
	args = append(args, "lint", "--reporter=json", "--max-diagnostics="+strconv.Itoa(maxBiomeDiagnostics),
		"--only=complexity/noExcessiveCognitiveComplexity", "--only=complexity/noExcessiveLinesPerFunction",
		"--no-errors-on-unmatched", "--config-path", configDir, "--")
	args = append(args, paths...)
	command := exec.CommandContext(ctx, binary, args...) //nolint:gosec // G204: execute the biome linter
	command.Dir = root
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	var exitErr *exec.ExitError
	if runErr != nil && (!errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1) {
		return biomeReport{}, fmt.Errorf("biome lint failed: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	var report biomeReport
	err := json.Unmarshal(stdout.Bytes(), &report)
	if err != nil {
		return biomeReport{}, fmt.Errorf("parse biome output: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if report.Summary == nil || report.Diagnostics == nil {
		return biomeReport{}, errBiomeMissingOutput
	}
	err = validateBiomeErrors(report)
	if err != nil {
		return biomeReport{}, err
	}
	return report, nil
}
