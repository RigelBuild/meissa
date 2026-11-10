// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"sort"
	"strconv"
	"strings"
)

// Render serializes the T1 file and directory entries in stable key order.
func Render(allow Allowlist) []byte {
	var output strings.Builder
	writeEntries(&output, "files", allow.Files)
	writeEntries(&output, "dirs", allow.Dirs)
	writeFunctionEntries(&output, allow.Functions)
	if len(allow.Files) == 0 && len(allow.Dirs) == 0 && len(allow.Functions) == 0 {
		output.WriteString("files: {}\n")
	}
	return []byte(output.String())
}

func writeEntries(output *strings.Builder, name string, entries map[string]Entry) {
	if len(entries) == 0 {
		return
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output.WriteString(name + ": {\n")
	for _, key := range keys {
		entry := entries[key]
		output.WriteString("\t" + strconv.Quote(key) + ": {value: " + strconv.Itoa(entry.Value) +
			", reason: " + strconv.Quote(entry.Reason) + "},\n")
	}
	output.WriteString("}\n")
}

func writeFunctionEntries(output *strings.Builder, functions map[Metric]map[string][]Entry) {
	for _, metric := range functionMetrics() {
		paths := functions[metric]
		if len(paths) == 0 {
			continue
		}
		output.WriteString("functions: " + strconv.Quote(string(metric)) + ": {\n")
		for _, path := range sortedEntryPaths(paths) {
			output.WriteString("\t" + strconv.Quote(path) + ": [\n")
			for _, entry := range sortedFunctionEntries(paths[path]) {
				output.WriteString("\t\t{id: " + strconv.Quote(entry.ID) + ", value: " + strconv.Itoa(entry.Value) +
					", reason: " + strconv.Quote(entry.Reason) + "},\n")
			}
			output.WriteString("\t],\n")
		}
		output.WriteString("}\n")
	}
}
