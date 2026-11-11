// SPDX-License-Identifier: MIT OR Apache-2.0

// Package format renders length gate findings as the lines the CLI prints.
package format

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/lengthgate/comment"
	"github.com/RigelBuild/meissa/go/internal/lengthgate/model"
)

// Finding returns the output lines for one finding.
func Finding(finding model.Finding, cfg model.Config) []string {
	switch f := finding.(type) {
	case model.CommentFinding:
		return []string{
			fmt.Sprintf("%s:%d: %d-line comment block (ceiling %d)", f.Path, f.Line, f.Lines, comment.Ceiling),
			fmt.Sprintf("Trim it to %d lines, or add a LENGTH marker with a reason.", comment.Ceiling),
		}
	case model.DocFinding:
		fix := "Split the document into a directory of files."
		if docMarkerAllowed(cfg, f.DocClass) {
			fix = "Trim it to the target, or add a LENGTH marker naming the section that carries the excess."
		}
		return []string{
			fmt.Sprintf("%s: %s prose words (%s target %s)",
				f.Path, groupThousands(f.Words), f.DocClass, groupThousands(int(f.Target))),
			fix,
		}
	case model.BadMarkerFinding:
		return []string{
			fmt.Sprintf("%s:%d: bad LENGTH marker: %s", f.Path, f.Line, f.Reason),
			"Fix the reason or § section, or remove the marker.",
		}
	case model.UnscannableFinding:
		return []string{
			f.Path + ": no scanner could place comments",
			"Fix the unclosed construct reported by the scanner, or add scanner support.",
		}
	}
	return nil
}

// docMarkerAllowed reports whether the named document class permits a LENGTH marker.
func docMarkerAllowed(cfg model.Config, name string) bool {
	for _, class := range cfg.Docs {
		if class.Name == name {
			return class.MarkerAllowed
		}
	}
	return false
}

// groupThousands formats n with "," between groups of three digits, as toLocaleString("en-US") does.
func groupThousands(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := strconv.Itoa(n)
	var out strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return sign + out.String()
}
