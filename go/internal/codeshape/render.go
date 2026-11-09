// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

import (
	"fmt"
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
	if len(allow.Functions) > 0 {
		metrics := make([]string, 0, len(allow.Functions))
		for metric := range allow.Functions {
			metrics = append(metrics, metric)
		}
		sort.Strings(metrics)
		output.WriteString("functions: {\n")
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
					fmt.Fprintf(&output, "\t\t\t{id: %s, value: %d, reason: %s},\n", strconv.Quote(entry.ID), entry.Value, strconv.Quote(entry.Reason))
				}
				output.WriteString("\t\t],\n")
			}
			output.WriteString("\t},\n")
		}
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
		fmt.Fprintf(output, "\t%q: {value: %d, reason: %q},\n", key, entry.Value, entry.Reason)
	}
	output.WriteString("}\n")
}
