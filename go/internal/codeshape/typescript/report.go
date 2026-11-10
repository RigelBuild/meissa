// SPDX-License-Identifier: MIT OR Apache-2.0

package typescript

import "github.com/odvcencio/gotreesitter"

// biomeReport is the JSON schema for Biome's lint output.
type biomeReport struct {
	Summary *struct {
		Changed               int `json:"changed"`
		Unchanged             int `json:"unchanged"`
		DiagnosticsNotPrinted int `json:"diagnosticsNotPrinted"`
		Errors                int `json:"errors"`
	} `json:"summary"`
	Diagnostics []struct {
		Category string `json:"category"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Location struct {
			Path  string `json:"path"`
			Start struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
		} `json:"location"`
	} `json:"diagnostics"`
}

type tsFunction struct {
	node *gotreesitter.Node
	id   string
}
