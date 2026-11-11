// SPDX-License-Identifier: MIT OR Apache-2.0

package ledger

import (
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// ParseDecisionFile parses a fixed four-key front matter block and resolves its record path.
func ParseDecisionFile(filePath string, src []byte) (DecisionRow, error) {
	block, err := parseFrontMatter(filePath, src)
	if err != nil {
		return DecisionRow{}, err
	}
	values, err := parseDecisionValues(filePath, block)
	if err != nil {
		return DecisionRow{}, err
	}
	row, err := decisionRow(filePath, values)
	if err != nil {
		return DecisionRow{}, err
	}
	var document any
	err = yaml.Unmarshal([]byte(strings.Join(block, "\n")), &document)
	if err != nil {
		return DecisionRow{}, malformedDecision(filePath, 2, "malformed YAML: "+err.Error())
	}
	return row, nil
}
