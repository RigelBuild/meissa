// SPDX-License-Identifier: MIT OR Apache-2.0

package cli

import (
	"strings"
	"testing"
)

func TestBiomeIgnoredCountedPath(t *testing.T) {
	for _, tc := range []struct{ name, rootConfig, nested string }{
		{"includes", strings.TrimSuffix(tsBiomeConfig, "}") + `,"files":{"includes":["**", "!sub/**"]}}`, ""},
		{"override", strings.TrimSuffix(tsBiomeConfig, "}") +
			`,"overrides":[{"includes":["sub/**"],"linter":{"enabled":false}}]}`, ""},
		{"nested", tsBiomeConfig, `{"root":false,"extends":"//","linter":{"enabled":false}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tsRepo(t, "function healthy() {}\n")
			trackedFile(t, root, "biome.json", tc.rootConfig)
			trackedFile(t, root, "sub/x.ts", "function large() {\n"+strings.Repeat("console.log(1);\n", 51)+"}\n")
			if tc.nested != "" {
				trackedFile(t, root, "sub/biome.json", tc.nested)
			}
			commitFiles(t, root)
			code, out, stderr := runGoGate(t, root)
			if code != 1 || !strings.Contains(out, "sub/x.ts") {
				t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
			}
		})
	}
}
