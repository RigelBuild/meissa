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
	if len(allow.Files) == 0 && len(allow.Dirs) == 0 && len(allow.Functions) == 0 {
		output.WriteString("files: {}\n")
	}
	metrics := make([]string, 0, len(allow.Functions))
	for metric := range allow.Functions {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	if len(metrics) > 0 {
		output.WriteString("functions: {\n")
	}
	for _, metric := range metrics {
		output.WriteString("\t" + strconv.Quote(metric) + ": {\n")
		paths := make([]string, 0, len(allow.Functions[metric]))
		for path := range allow.Functions[metric] {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			output.WriteString("\t\t" + strconv.Quote(path) + ": [\n")
			entries := append([]FunctionEntry(nil), allow.Functions[metric][path]...)
			sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
			for _, entry := range entries {
				writeFunctionEntry(&output, entry)
			}
			output.WriteString("\t\t],\n")
		}
		output.WriteString("\t},\n")
	}
	if len(metrics) > 0 {
		output.WriteString("}\n")
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

func writeFunctionEntry(output *strings.Builder, entry FunctionEntry) {
	output.WriteString("\t\t\t{id: " + strconv.Quote(entry.ID) + ", value: " + strconv.Itoa(entry.Value) +
		", reason: " + strconv.Quote(entry.Reason) + "},\n")
}
