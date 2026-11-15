// SPDX-License-Identifier: MIT OR Apache-2.0

// Package dllist ports the dl-list CLI: it lists design-ledger decision files
// with surface, area, status, text and glob filters.
package dllist

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
	"github.com/RigelBuild/meissa/go/internal/ledgergate/core"
	"github.com/RigelBuild/meissa/go/ledger"
)

//nolint:gochecknoglobals // Shared CLI choices and validation must stay in sync.
var statuses = []string{"active", "superseded", "retired"}

// Filters narrow the listed decisions. Empty fields do not filter.
type Filters struct {
	Surface string
	Area    string
	Status  string
	Grep    string
}

// DecisionFile is one decision file: its repository path and its text.
type DecisionFile struct {
	Path string
	Text string
}

// location returns "<surface>/<area>" at surface depth 1, or "<area>" at depth 0.
func location(path string, surfaceDepth int) string {
	parts := strings.Split(path, "/")
	decisionsIndex := lastIndex(parts, "decisions")
	area := elementAt(parts, decisionsIndex+1)
	if surfaceDepth == 0 {
		return area
	}
	surface := elementAt(parts, decisionsIndex-1)
	return surface + "/" + area
}

// lastIndex returns the index of the last element equal to want, or -1.
func lastIndex(parts []string, want string) int {
	for index := range slices.Backward(parts) {
		if parts[index] == want {
			return index
		}
	}
	return -1
}

// elementAt returns parts[index], or "" when index is out of range (the TS `?? ""`).
func elementAt(parts []string, index int) string {
	if index < 0 || index >= len(parts) {
		return ""
	}
	return parts[index]
}

// format renders one decision as a listing line.
func format(row ledger.DecisionRow, surfaceDepth int) string {
	status := stripSupersessionDate(row.Status)
	anchor := ""
	if row.RecordAnchor != "" {
		anchor = "#" + row.RecordAnchor
	}
	return row.ID + "  " + status + "  " + location(row.Path, surfaceDepth) + "  " +
		row.Decision + "  → " + row.RecordPath + anchor
}

// stripSupersessionDate removes a trailing " (...)" from a status, as the TS
// `replace(/ \(.+\)$/, "")` does.
func stripSupersessionDate(status string) string {
	open := strings.Index(status, " (")
	// The TS pattern " \(.+\)$" needs at least one character between the parens.
	if open < 0 || !strings.HasSuffix(status, ")") || len(status) < open+4 {
		return status
	}
	return status[:open]
}

// surfaceOf returns the surface a row belongs to. At depth 0 it is the only
// configured surface, when there is exactly one.
func surfaceOf(where string, cfg core.Config) string {
	if cfg.SurfaceDepth == 0 {
		if len(cfg.Surfaces) == 1 {
			return cfg.Surfaces[0]
		}
		return ""
	}
	return strings.SplitN(where, "/", 2)[0]
}

// matches reports whether a row passes every filter. line is the formatted listing line.
func matches(row ledger.DecisionRow, line string, f Filters, cfg core.Config) bool {
	where := location(row.Path, cfg.SurfaceDepth)
	if f.Surface != "" && surfaceOf(where, cfg) != f.Surface {
		return false
	}
	if f.Area != "" && lastSegment(where) != f.Area {
		return false
	}
	if f.Status != "" && !strings.HasPrefix(jstext.ToLower(row.Status), f.Status) {
		return false
	}
	if f.Grep != "" && !strings.Contains(jstext.ToLower(line), jstext.ToLower(f.Grep)) {
		return false
	}
	return true
}

func lastSegment(where string) string {
	return where[strings.LastIndex(where, "/")+1:]
}

// ListDecisions parses, filters and formats decision files. A malformed file
// becomes an error line; the rest are still listed.
func ListDecisions(files []DecisionFile, f Filters, cfg core.Config) ([]string, []string) {
	var rows []ledger.DecisionRow
	var lines, errs []string
	for _, file := range files {
		row, err := ledger.ParseDecisionFile(file.Path, []byte(file.Text))
		if err != nil {
			errs = append(errs, malformedLine(err))
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return idNumber(rows[i].ID) < idNumber(rows[j].ID)
	})
	for _, row := range rows {
		line := format(row, cfg.SurfaceDepth)
		if matches(row, line, f, cfg) {
			lines = append(lines, line)
		}
	}
	return lines, errs
}

// idNumber returns the number in a "DL-<n>" id, for sorting.
func idNumber(id string) int {
	number, err := strconv.Atoi(strings.TrimPrefix(id, "DL-"))
	if err != nil {
		return 0
	}
	return number
}

// malformedLine is the TS "path:line: reason" text, which MalformedDecision.Error produces.
func malformedLine(err error) string {
	return err.Error()
}

// isStatus reports whether value is one of the statuses --status accepts.
func isStatus(value string) bool {
	return slices.Contains(statuses, value)
}
