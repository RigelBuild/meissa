// SPDX-License-Identifier: MIT OR Apache-2.0

package ledger

import (
	"path"
	"sort"
	"strings"
)

// RenderDecisionIndex sorts rows by numeric id and emits a Markdown table.
func RenderDecisionIndex(rows []DecisionRow) string {
	sorted := append([]DecisionRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := numericID(sorted[i].ID), numericID(sorted[j].ID)
		if left != right {
			return left < right
		}
		return sorted[i].Path < sorted[j].Path
	})
	var output strings.Builder
	output.WriteString("| ID | Decision | Status | Record |\n|---|---|---|---|\n")
	for _, row := range sorted {
		output.WriteString("| [")
		output.WriteString(row.ID)
		output.WriteString("](")
		output.WriteString(path.Base(row.Path))
		output.WriteString(") | ")
		writeCell(&output, row.Decision)
		output.WriteString(" | ")
		writeCell(&output, row.Status)
		output.WriteString(" | [")
		writeCell(&output, row.RecordRaw)
		output.WriteString("](")
		output.WriteString(row.RecordRaw)
		output.WriteString(") |\n")
	}
	return output.String()
}

func writeCell(output *strings.Builder, value string) {
	for index := 0; index < len(value); {
		if value[index] == '\\' {
			start := index
			for index < len(value) && value[index] == '\\' {
				index++
			}
			output.WriteString(value[start:index])
			if index < len(value) && value[index] == '|' {
				output.WriteString(value[start:index])
			}
			continue
		}
		if value[index] == '|' {
			output.WriteByte('\\')
		}
		output.WriteByte(value[index])
		index++
	}
}
