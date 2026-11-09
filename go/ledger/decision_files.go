// SPDX-License-Identifier: MIT OR Apache-2.0

// Package ledger parses decision files and renders decision indexes.
package ledger

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

const keyCount = 4

var (
	keyLineRE = regexp.MustCompile(`^([A-Za-z_]+): (.*)$`)
	idRE      = regexp.MustCompile(`^DL-(?:\d{3}|[1-9]\d{3,})$`)
	fileRE    = regexp.MustCompile(`^(DL-(?:\d{3}|[1-9]\d{3,}))\.md$`)
	recordRE  = regexp.MustCompile(`^(?:[A-Za-z0-9_-][A-Za-z0-9._-]*\/|\.\.?\/)*[A-Za-z0-9_-][A-Za-z0-9._-]*\.md$`)
	anchorRE  = regexp.MustCompile(`^[\p{Ll}\p{Nd}_-]+$`)
	//nolint:gochecknoglobals // These compiled patterns are reused for each parsed file.
	statusREs = []*regexp.Regexp{
		regexp.MustCompile(`^Active \(.+, \d{4}-\d{2}-\d{2}\)$`),
		regexp.MustCompile(`^Superseded by DL-(?:\d{3}|[1-9]\d{3,}) \(.+, \d{4}-\d{2}-\d{2}\)$`),
		regexp.MustCompile(`^Retired \(.+, \d{4}-\d{2}-\d{2}\)$`),
	}
)

// DecisionRow is one successfully parsed decision file.
type DecisionRow struct {
	Path         string
	Line         int
	ID           string
	Decision     string
	Status       string
	RecordRaw    string
	RecordPath   string
	RecordAnchor string
}

// MalformedDecision describes a decision file that failed validation.
//
//nolint:errname // The public type name is fixed by the migration contract.
type MalformedDecision struct {
	Path   string
	Line   int
	Reason string
}

// Error formats the path, one-based line number, and parse reason.
func (e *MalformedDecision) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Reason)
}

func malformedDecision(filePath string, line int, reason string) *MalformedDecision {
	return &MalformedDecision{Path: filePath, Line: line, Reason: reason}
}

func keyAt(index int) string {
	switch index {
	case 0:
		return "id"
	case 1:
		return "decision"
	case 2:
		return "status"
	default:
		return "record"
	}
}

func parseFrontMatter(filePath string, src []byte) ([]string, error) {
	lines := strings.Split(strings.TrimPrefix(string(src), "\ufeff"), "\n")
	if jstext.TrimRight(lines[0]) != "---" {
		return nil, malformedDecision(
			filePath,
			1,
			"file must start with a `---` front-matter block",
		)
	}
	closeLine := -1
	for index := 1; index < len(lines); index++ {
		if jstext.TrimRight(lines[index]) == "---" {
			closeLine = index
			break
		}
	}
	if closeLine < 0 {
		return nil, malformedDecision(filePath, 1, "front-matter block is not closed with `---`")
	}
	block := lines[1:closeLine]
	if len(block) != keyCount {
		return nil, malformedDecision(
			filePath,
			2,
			"front matter must hold exactly id, decision, status, record",
		)
	}
	return block, nil
}

func parseDecisionValues(filePath string, block []string) ([keyCount]string, error) {
	var values [keyCount]string
	for index := range keyCount {
		key := keyAt(index)
		lineNumber := index + 2
		matches := keyLineRE.FindStringSubmatch(jstext.TrimRight(block[index]))
		if matches == nil || matches[1] != key {
			return values, malformedDecision(
				filePath,
				lineNumber,
				fmt.Sprintf("expected key `%s` at line %d", key, lineNumber),
			)
		}
		raw := matches[2]
		if key == "decision" || key == "status" {
			if !isQuotedJSONString(raw) {
				return values, quotedValueError(filePath, lineNumber, key)
			}
			err := json.Unmarshal([]byte(raw), &values[index])
			if err != nil {
				return values, quotedValueError(filePath, lineNumber, key)
			}
			if hasLineBreak(values[index]) {
				return values, malformedDecision(
					filePath,
					lineNumber,
					fmt.Sprintf("`%s` must be one line", key),
				)
			}
			continue
		}
		if raw == "" || isQuotedOrTagged(raw[0]) {
			return values, malformedDecision(
				filePath,
				lineNumber,
				fmt.Sprintf("`%s` must be a plain unquoted value", key),
			)
		}
		values[index] = raw
	}
	return values, nil
}

func quotedValueError(filePath string, line int, key string) *MalformedDecision {
	return malformedDecision(
		filePath,
		line,
		fmt.Sprintf("`%s` must be a one-line double-quoted string", key),
	)
}

func isQuotedJSONString(raw string) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	for index := 1; index < len(raw)-1; {
		if raw[index] == '\\' {
			next, ok := quotedEscapeEnd(raw, index)
			if !ok {
				return false
			}
			index = next
			continue
		}
		r, size := utf8.DecodeRuneInString(raw[index:])
		if r == '"' || unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
		index += size
	}
	return true
}

func quotedEscapeEnd(raw string, slash int) (int, bool) {
	escape := slash + 1
	if escape >= len(raw)-1 {
		return 0, false
	}
	switch raw[escape] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return escape + 1, true
	case 'u':
		end := escape + 5
		if end > len(raw)-1 {
			return 0, false
		}
		for hexIndex := escape + 1; hexIndex <= end-1; hexIndex++ {
			if !isHex(raw[hexIndex]) {
				return 0, false
			}
		}
		return end, true
	default:
		return 0, false
	}
}

func isHex(character byte) bool {
	isDigit := character >= '0' && character <= '9'
	isLowerHex := character >= 'a' && character <= 'f'
	isUpperHex := character >= 'A' && character <= 'F'
	return isDigit || isLowerHex || isUpperHex
}

func hasLineBreak(text string) bool {
	for _, character := range text {
		if unicode.IsControl(character) || character == '\u2028' || character == '\u2029' {
			return true
		}
	}
	return false
}

func isQuotedOrTagged(first byte) bool {
	switch first {
	case '"', '\'', '[', '{', '|', '>', '&', '*', '!', '%', '@', '`', '#':
		return true
	default:
		return false
	}
}

func matchesStatus(status string) bool {
	for _, pattern := range statusREs {
		if pattern.MatchString(status) {
			return true
		}
	}
	return false
}

func decisionRow(filePath string, values [keyCount]string) (DecisionRow, error) {
	id := values[0]
	if !idRE.MatchString(id) {
		return DecisionRow{}, malformedDecision(
			filePath,
			2,
			fmt.Sprintf("id `%s` is not canonical DL-NNN", id),
		)
	}
	baseName := path.Base(filePath)
	fileMatch := fileRE.FindStringSubmatch(baseName)
	if fileMatch == nil || fileMatch[1] != id {
		return DecisionRow{}, malformedDecision(
			filePath,
			2,
			fmt.Sprintf("id `%s` does not match file name `%s`", id, baseName),
		)
	}
	decision := values[1]
	if jstext.Trim(decision) == "" {
		return DecisionRow{}, malformedDecision(filePath, 3, "`decision` is empty")
	}
	status := values[2]
	if !matchesStatus(status) {
		return DecisionRow{}, malformedDecision(
			filePath,
			4,
			fmt.Sprintf(
				"status `%s` is not Active, Superseded by DL-NNN, or Retired with (<who>, YYYY-MM-DD)",
				status,
			),
		)
	}
	return decisionRecord(filePath, id, decision, status, values[3])
}

func decisionRecord(filePath, id, decision, status, recordRaw string) (DecisionRow, error) {
	target, anchor, hasAnchor := strings.Cut(recordRaw, "#")
	if !recordRE.MatchString(target) {
		return DecisionRow{}, malformedDecision(
			filePath,
			5,
			"`record` must be a relative `.md` path of letters, digits, `.`, `_`, `-` and `/`",
		)
	}
	if hasAnchor && !anchorRE.MatchString(anchor) {
		return DecisionRow{}, malformedDecision(
			filePath,
			5,
			fmt.Sprintf("record anchor `#%s` is not a heading slug", anchor),
		)
	}
	recordPath := path.Clean(path.Join(path.Dir(filePath), target))
	if strings.HasPrefix(recordPath, "..") {
		return DecisionRow{}, malformedDecision(filePath, 5, "`record` climbs above the repository root")
	}
	return DecisionRow{
		Path: filePath, Line: 2, ID: id, Decision: decision, Status: status,
		RecordRaw: recordRaw, RecordPath: recordPath, RecordAnchor: anchor,
	}, nil
}

func numericID(id string) float64 {
	if len(id) < 3 {
		return 0
	}
	number, err := strconv.ParseFloat(id[3:], 64)
	if err != nil {
		return 0
	}
	return number
}
