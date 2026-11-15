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

// maxKeyCount is the front-matter key count with the optional title.
const maxKeyCount = 5

// titleIndex is the front-matter index of the optional title key, after record.
const titleIndex = 4

var (
	// JS `.` also stops at \r, U+2028 and U+2029; Go's stops only at \n.
	keyLineRE = regexp.MustCompile(`^([A-Za-z_]+): ([^\r\n\x{2028}\x{2029}]*)$`)
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
	// Title is the optional one-line decision title; "" means no title.
	Title string
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
	case 3:
		return "record"
	default:
		return "title"
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
	if len(block) != keyCount && (len(block) != maxKeyCount || !strings.HasPrefix(block[titleIndex], "title: ")) {
		return nil, malformedDecision(
			filePath,
			2,
			"front matter must hold id, decision, status, record and optional title",
		)
	}
	return block, nil
}

func parseDecisionValues(filePath string, block []string) ([maxKeyCount]string, error) {
	var values [maxKeyCount]string
	for index := range block {
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
		if key == "decision" || key == "status" || key == "title" {
			value, err := parseQuotedDecisionValue(filePath, lineNumber, key, raw)
			if err != nil {
				return values, err
			}
			values[index] = value
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

func parseQuotedDecisionValue(filePath string, line int, key, raw string) (string, error) {
	if !isQuotedJSONString(raw) {
		return "", quotedValueError(filePath, line, key)
	}
	var value string
	var err error
	err = json.Unmarshal([]byte(raw), &value)
	if err != nil {
		return "", quotedValueError(filePath, line, key)
	}
	if hasLineBreak(value) {
		return "", malformedDecision(
			filePath,
			line,
			fmt.Sprintf("`%s` must be one line", key),
		)
	}
	err = validateDecisionTitle(filePath, line, key, value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func validateDecisionTitle(filePath string, line int, key, title string) error {
	if key == "title" {
		return checkTitle(filePath, line, title)
	}
	return nil
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

func decisionRow(filePath string, values [maxKeyCount]string) (DecisionRow, error) {
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
	row, err := decisionRecord(filePath, id, decision, status, values[3])
	if err != nil {
		return DecisionRow{}, err
	}
	row.Title = values[titleIndex]
	return row, nil
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
		RecordRaw: recordRaw, RecordPath: recordPath, RecordAnchor: anchor, Title: "",
	}, nil
}

var (
	titleTokenRE = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b|\b\d{4}-\d{2}-\d{2}\b`)
)

// checkTitle enforces the optional title rules: 8 to 60 characters, no id,
// issue key or date, and no trailing period. Length counts decoded runes.
func checkTitle(filePath string, line int, title string) error {
	length := utf8.RuneCountInString(title)
	if length < 8 || length > 60 {
		return malformedDecision(filePath, line, "`title` must be 8 to 60 characters")
	}
	if titleTokenRE.MatchString(title) {
		return malformedDecision(filePath, line, "`title` must not hold an id, issue key or date")
	}
	if strings.HasSuffix(title, ".") {
		return malformedDecision(filePath, line, "`title` must not end with a period")
	}
	return nil
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

// RecordSlug returns the lowercased record base name without .md. A design.md
// record takes its parent directory name, so platform/oss-forks/fork-releases/design.md
// gives fork-releases and product/rigel/README.md gives readme.
func RecordSlug(recordPath string) string {
	base := path.Base(recordPath)
	if base == "design.md" {
		base = path.Base(path.Dir(recordPath))
	}
	return strings.ToLower(strings.TrimSuffix(base, ".md"))
}
