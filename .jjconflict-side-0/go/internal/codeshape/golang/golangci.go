// SPDX-License-Identifier: MIT OR Apache-2.0

package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

var (
	gocognitValue      = regexp.MustCompile(`cognitive complexity ([0-9]+) of func`)
	funlenValue        = regexp.MustCompile(`\(([0-9]+) > [0-9]+\)`)
	errGoLintMissing   = errors.New("measurement omitted")
	errGoMetricParse   = errors.New("cannot parse value")
	errGoMetricInvalid = errors.New("invalid value")
)

//nolint:tagliatelle // external wire format
type golangciReport struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
	Report struct {
		Error   string `json:"Error"`
		Linters []struct {
			Name    string `json:"Name"`
			Enabled bool   `json:"Enabled"`
		} `json:"Linters"`
	} `json:"Report"`
}

func runGolangci(
	ctx context.Context,
	binary, root, module, linter string,
	lines, statements int,
	cfg codeshape.Config,
) (golangciReport, error) {
	configuration := fmt.Sprintf(
		"version: '2'\nlinters:\n  default: none\n  enable:\n    - %s\n"+
			"  settings:\n    gocognit:\n      min-complexity: %d\n    funlen:\n"+
			"      lines: %d\n      statements: %d\n      ignore-comments: true\n"+
			"issues:\n  max-issues-per-linter: 0\n  max-same-issues: 0\n",
		linter,
		cfg.Functions.Go.Cognitive,
		lines,
		statements,
	)
	temporary, err := os.CreateTemp("", "code-shape-golangci-*.yml")
	if err != nil {
		return golangciReport{}, fmt.Errorf("create golangci-lint config: %w", err)
	}
	report, runErr := runGolangciWithConfig(ctx, binary, root, module, linter, configuration, temporary)
	removeErr := os.Remove(temporary.Name())
	if removeErr != nil {
		removeErr = fmt.Errorf("remove golangci-lint config: %w", removeErr)
	}
	return report, errors.Join(runErr, removeErr)
}

func runGolangciWithConfig(
	ctx context.Context, binary, root, module, linter, configuration string, temporary *os.File,
) (golangciReport, error) {
	var err error
	_, err = temporary.WriteString(configuration)
	if err != nil {
		closeErr := temporary.Close()
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close golangci-lint config: %w", closeErr))
		}
		return golangciReport{}, fmt.Errorf("write golangci-lint config: %w", err)
	}
	err = temporary.Close()
	if err != nil {
		return golangciReport{}, fmt.Errorf("close golangci-lint config: %w", err)
	}
	//nolint:gosec // G204: execute the configured golangci-lint binary
	command := exec.CommandContext(ctx, binary, "run", "--config", temporary.Name(), "--enable-only", linter,
		"--output.json.path", "stdout", "--path-mode", "abs", "--show-stats=false", "--uniq-by-line=false",
		"--max-issues-per-linter", "0", "--max-same-issues", "0", "./...")
	command.Dir = filepath.Join(root, filepath.FromSlash(module))
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) || exitError.ExitCode() != 1 {
			return golangciReport{}, fmt.Errorf("golangci-lint %s failed in %q: %w: %s",
				linter, module, runErr, strings.TrimSpace(stderr.String()))
		}
	}
	var report golangciReport
	err = json.Unmarshal(stdout.Bytes(), &report)
	if err != nil {
		return golangciReport{}, fmt.Errorf("parse golangci-lint %s output in %q: %w", linter, module, err)
	}
	if report.Report.Error != "" {
		return golangciReport{}, fmt.Errorf("golangci-lint %s %w: %s", linter, errGoLintReport, report.Report.Error)
	}
	if runErr != nil && len(report.Issues) == 0 {
		return golangciReport{}, fmt.Errorf("golangci-lint %s failed without issues: %w", linter, runErr)
	}
	enabled := false
	for _, entry := range report.Report.Linters {
		if entry.Name == linter && entry.Enabled {
			enabled = true
		}
	}
	if !enabled {
		return golangciReport{}, fmt.Errorf("golangci-lint output omitted enabled %s measurement: %w",
			linter, errGoLintMissing)
	}
	return report, nil
}

func metricValue(text string, metric codeshape.Metric) (int, error) {
	pattern := funlenValue
	if metric == codeshape.MetricGoCognitive {
		pattern = gocognitValue
	}
	matches := pattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, fmt.Errorf("cannot parse %s value from %q: %w", metric, text, errGoMetricParse)
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s value in %q: %w", metric, text, errGoMetricInvalid)
	}
	return value, nil
}
