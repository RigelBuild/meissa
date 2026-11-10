// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"fmt"
	"io"
	"maps"

	"github.com/RigelBuild/meissa/go/internal/codeshape"
)

func handleTSSkips(output io.Writer, skips []codeshape.Skip, cfg codeshape.Config) (
	[]codeshape.Finding, []string, error,
) {
	findings := []codeshape.Finding{}
	paths := []string{}
	for _, skip := range skips {
		if skip.Unmeasured != nil {
			paths = append(paths, skip.Path)
			if skip.Unmeasured.Ignored {
				findings = append(findings, codeshape.Finding{Path: skip.Path, Metric: codeshape.MetricUnmeasured,
					Value: 0, Limit: 0, Fix: skip.Reason + "; exclude it or fix the parse"})
			}
			for metric, value := range skip.Unmeasured.Values {
				findings = append(findings, codeshape.Finding{Path: skip.Path, Metric: metric, Value: value,
					Limit: cfg.Functions.LimitFor(metric), Fix: "unparseable by the function collector; exclude it or fix the parse"})
			}
		}
		_, err := fmt.Fprintf(output, "code-shape-gate: skip %s: %s\n", skip.Path, skip.Reason)
		if err != nil {
			return nil, nil, fmt.Errorf("write skip: %w", err)
		}
	}
	return findings, paths, nil
}

func separateUnmeasured(allow *codeshape.Allowlist, paths []string) codeshape.FunctionMeasurements {
	preserved := make(codeshape.FunctionMeasurements)
	for metric, entries := range allow.Functions {
		copyEntries := make(map[string][]codeshape.Entry, len(entries))
		maps.Copy(copyEntries, entries)
		allow.Functions[metric] = copyEntries
		for _, path := range paths {
			if functions, ok := copyEntries[path]; ok {
				if preserved[metric] == nil {
					preserved[metric] = map[string][]codeshape.Entry{}
				}
				preserved[metric][path] = functions
				delete(copyEntries, path)
			}
		}
	}
	return preserved
}

func restoreUnmeasured(allow *codeshape.Allowlist, preserved codeshape.FunctionMeasurements) {
	for metric, paths := range preserved {
		if allow.Functions[metric] == nil {
			allow.Functions[metric] = map[string][]codeshape.Entry{}
		}
		maps.Copy(allow.Functions[metric], paths)
	}
}

func handleGoSkips(output io.Writer, skips []codeshape.Skip) ([]codeshape.Finding, []string, error) {
	var findings []codeshape.Finding
	var paths []string
	for _, skip := range skips {
		_, err := fmt.Fprintf(output, "code-shape-gate: skip %s: %s\n", skip.Path, skip.Reason)
		if err != nil {
			return nil, nil, fmt.Errorf("write skip: %w", err)
		}
		if skip.Unmeasured != nil {
			paths = append(paths, skip.Path)
			findings = append(findings, codeshape.Finding{Path: skip.Path, Metric: codeshape.MetricUnmeasured,
				Value: 0, Limit: 0, Fix: skip.Reason + "; exclude it or include it in the default Go build"})
		}
	}
	return findings, paths, nil
}
