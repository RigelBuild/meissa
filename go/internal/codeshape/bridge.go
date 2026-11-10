// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"context"

	"cuelang.org/go/cue/cuecontext"
)

func TSPolicyFindings(ctx context.Context, root string, cfg Config) ([]Finding, error) {
	return tsPolicyFindings(ctx, root, cfg)
}

func EmptyAllowlist() Allowlist { return emptyAllowlist() }

func DecodeBaseAllowlist(data []byte) (Allowlist, error) {
	ctx := cuecontext.New()
	schema, err := schemaValue(ctx)
	if err != nil {
		return Allowlist{}, err
	}
	return decodeAllowlistBytes(ctx, schema, data, "base allowlist")
}

func RewriteFunctionEntries(updated, allow, values FunctionMeasurements, cfg Config, seed bool) (bool, []string) {
	return rewriteFunctionEntries(updated, allow, values, cfg, seed)
}

func (cfg Config) LimitFor(metric Metric, path string) int { return cfg.limitFor(metric, path) }
func DisplayPath(metric Metric, path string) string        { return displayPath(metric, path) }
func SortFindings(findings []Finding)                      { sortFindings(findings) }
