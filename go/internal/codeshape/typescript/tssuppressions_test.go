// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import "testing"

func TestBiomeSuppressionGroups(t *testing.T) {
	for _, directive := range []string{
		"biome-ignore lint: reason", "biome-ignore lint/complexity: reason",
		"biome-ignore-all lint: reason", "biome-ignore-start lint/complexity: reason",
	} {
		t.Run(directive, func(t *testing.T) {
			findings := tsDirectiveFindings("x.ts", []byte("// "+directive))
			if len(findings) != 2 || findings[0].Value != 1 || findings[1].Value != 1 {
				t.Fatalf("findings = %#v", findings)
			}
		})
	}
}
