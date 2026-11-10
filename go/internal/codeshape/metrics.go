// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

// Metric identifies one measured code-shape dimension.
type Metric string

// Metric constants identify the supported code-shape dimensions.
const (
	MetricFiles        Metric = "files"
	MetricDirs         Metric = "dirs"
	MetricGoCognitive  Metric = "go-cognitive"
	MetricGoLines      Metric = "go-lines"
	MetricGoStatements Metric = "go-statements"
	MetricTSCognitive  Metric = "ts-cognitive"
	MetricTSLines      Metric = "ts-lines"
	MetricUnmeasured   Metric = "unmeasured"
)

// FunctionMeasurements maps each metric and source path to individual functions.
type FunctionMeasurements = map[Metric]map[string][]Entry
