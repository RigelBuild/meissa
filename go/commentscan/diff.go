// Package commentscan parses unified Git diffs for comment scanning.
// SPDX-License-Identifier: MIT OR Apache-2.0
package commentscan

import (
	"strings"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// DiffLine is one context, added, or removed line in a diff hunk.
type DiffLine struct {
	Kind  byte // ' ', '+' or '-'
	Text  string
	OldNo int
	NewNo int
}

// Hunk contains the changed lines for one range in a file.
type Hunk struct{ Lines []DiffLine }

// FileDiff contains paths and hunks for one file in a Git diff.
type FileDiff struct {
	Path, OldPath, NewPath string
	Hunks                  []Hunk
	Added, Deleted         bool // the file is new / the file is removed
	Binary, MetadataOnly   bool
}

// ExtOf returns the lowercased extension of the last path component.
func ExtOf(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		return ""
	}
	return jstext.ToLower(base[dot:])
}

func cEscape(code byte) (byte, bool) {
	switch code {
	case 'a':
		return 7, true
	case 'b':
		return 8, true
	case 'f':
		return 12, true
	case 'n':
		return 10, true
	case 'r':
		return 13, true
	case 't':
		return 9, true
	case 'v':
		return 11, true
	default:
		return 0, false
	}
}

// unquotePath decodes Git's quoted paths. Octal escapes encode UTF-8 bytes.
func unquotePath(raw string) string {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return raw
	}

	quoted := raw[1 : len(raw)-1]
	decoded := make([]byte, 0, len(quoted))
	for i := 0; i < len(quoted); {
		if quoted[i] != '\\' || i+1 >= len(quoted) {
			decoded = append(decoded, quoted[i])
			i++
			continue
		}

		next := quoted[i+1]
		if next >= '0' && next <= '3' && i+3 < len(quoted) && isOctal(quoted[i+2]) && isOctal(quoted[i+3]) {
			value := (next-'0')*64 + (quoted[i+2]-'0')*8 + quoted[i+3] - '0'
			decoded = append(decoded, value)
			i += 4
			continue
		}

		if value, ok := cEscape(next); ok {
			decoded = append(decoded, value)
		} else {
			decoded = append(decoded, next)
		}
		i += 2
	}

	return strings.ToValidUTF8(string(decoded), "\uFFFD")
}

func isOctal(value byte) bool {
	return value >= '0' && value <= '7'
}

// diffLinePath removes Git's tab terminator for paths that end in a space.
func diffLinePath(rest string) string {
	return unquotePath(strings.TrimSuffix(rest, "\t"))
}

func stripPrefix(path string) string {
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

// parseHeaderPath returns the b-side path in a diff --git header.
func parseHeaderPath(line string) string {
	rest := strings.TrimSuffix(line[len("diff --git "):], "\r")
	if strings.HasSuffix(rest, "\"") {
		open := strings.LastIndex(rest, " \"")
		if open != -1 {
			return stripPrefix(unquotePath(rest[open+1:]))
		}
	}

	middle := len(rest) / 2
	if middle >= 2 && middle+3 <= len(rest) && rest[middle] == ' ' && rest[2:middle] == rest[middle+3:] {
		return rest[middle+3:]
	}

	at := strings.LastIndex(rest, " b/")
	if at == -1 {
		return ""
	}
	return stripPrefix(unquotePath(rest[at+1:]))
}

// parseHunkHeader parses the old and new starting line numbers.
func parseHunkHeader(line string) (int, int, bool) {
	if !strings.HasPrefix(line, "@@ -") {
		return 0, 0, false
	}
	oldNo, next, ok := parseDecimal(line, len("@@ -"))
	if !ok {
		return 0, 0, false
	}
	next, ok = skipRangeCount(line, next)
	if !ok || !strings.HasPrefix(line[next:], " +") {
		return 0, 0, false
	}
	newNo, next, ok := parseDecimal(line, next+2)
	if !ok {
		return 0, 0, false
	}
	next, ok = skipRangeCount(line, next)
	if !ok || !strings.HasPrefix(line[next:], " @@") {
		return 0, 0, false
	}
	return oldNo, newNo, true
}

func parseDecimal(text string, start int) (int, int, bool) {
	if start >= len(text) || text[start] < '0' || text[start] > '9' {
		return 0, start, false
	}
	const maxInt = int(^uint(0) >> 1)
	value := 0
	for start < len(text) && text[start] >= '0' && text[start] <= '9' {
		digit := int(text[start] - '0')
		if value > (maxInt-digit)/10 {
			value = maxInt
		} else {
			value = value*10 + digit
		}
		start++
	}
	return value, start, true
}

func skipRangeCount(text string, start int) (int, bool) {
	if start >= len(text) || text[start] != ',' {
		return start, true
	}
	_, next, ok := parseDecimal(text, start+1)
	return next, ok
}

// ParseDiff parses file headers and hunks from a unified Git diff.
func ParseDiff(diff string) []FileDiff {
	files := make([]FileDiff, 0)
	lines := strings.Split(diff, "\n")
	var file *FileDiff
	oldPath, newPath, headerPath, fromPath := "", "", "", ""
	var hunk *Hunk
	beforeFirstHunk := true
	var renameFrom, renameTo, copyFrom, copyTo bool
	var oldMode, newMode, newFile, deletedFile bool
	oldNo, newNo := 0, 0

	finishFile := func() {
		if file == nil {
			return
		}
		file.Added = oldPath == "/dev/null" || newFile
		file.Deleted = newPath == "/dev/null" || deletedFile
		if oldPath != "" && oldPath != "/dev/null" {
			file.OldPath = stripPrefix(oldPath)
		} else {
			file.OldPath = fromPath
			if file.OldPath == "" {
				file.OldPath = headerPath
			}
		}
		if newPath != "" && newPath != "/dev/null" {
			file.NewPath = stripPrefix(newPath)
		} else {
			file.NewPath = headerPath
		}
		file.Path = file.NewPath
		if file.Path == "" {
			file.Path = file.OldPath
		}
		if file.Path == "" {
			file.Path = headerPath
		}
		file.MetadataOnly = renameFrom && renameTo || copyFrom && copyTo || oldMode && newMode
		files = append(files, *file)
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			finishFile()
			file = &FileDiff{
				Path: "", OldPath: "", NewPath: "", Hunks: make([]Hunk, 0),
				Added: false, Deleted: false, Binary: false, MetadataOnly: false,
			}
			hunk = nil
			beforeFirstHunk = true
			renameFrom, renameTo, copyFrom, copyTo = false, false, false, false
			oldMode, newMode, newFile, deletedFile = false, false, false, false
			oldPath, newPath, fromPath = "", "", ""
			headerPath = parseHeaderPath(line)
			continue
		}
		if file == nil {
			continue
		}
		if parsedOld, parsedNew, ok := parseHunkHeader(line); ok {
			oldNo, newNo = parsedOld, parsedNew
			file.Hunks = append(file.Hunks, Hunk{Lines: make([]DiffLine, 0)})
			hunk = &file.Hunks[len(file.Hunks)-1]
			beforeFirstHunk = false
			continue
		}
		if beforeFirstHunk {
			header := strings.TrimSuffix(line, "\r")
			switch {
			case strings.HasPrefix(header, "rename from "):
				renameFrom = true
				fromPath = unquotePath(header[len("rename from "):])
			case strings.HasPrefix(header, "rename to "):
				renameTo = true
				headerPath = unquotePath(header[len("rename to "):])
			case strings.HasPrefix(header, "copy from "):
				copyFrom = true
				fromPath = unquotePath(header[len("copy from "):])
			case strings.HasPrefix(header, "copy to "):
				copyTo = true
				headerPath = unquotePath(header[len("copy to "):])
			case strings.HasPrefix(header, "old mode "):
				oldMode = true
			case strings.HasPrefix(header, "new mode "):
				newMode = true
			case strings.HasPrefix(header, "new file mode "):
				newFile = true
			case strings.HasPrefix(header, "deleted file mode "):
				deletedFile = true
			case strings.HasPrefix(header, "Binary files "), strings.HasPrefix(header, "GIT binary "):
				file.Binary = true
			case strings.HasPrefix(header, "--- "):
				oldPath = diffLinePath(header[len("--- "):])
			case strings.HasPrefix(header, "+++ "):
				newPath = diffLinePath(header[len("+++ "):])
			}
			continue
		}
		if hunk == nil || strings.HasPrefix(line, "\\") {
			continue
		}
		if line == "" {
			hunk = nil
			continue
		}

		lineText := line[1:]
		switch line[0] {
		case ' ':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: ' ', Text: lineText, OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
		case '+':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: '+', Text: lineText, OldNo: oldNo, NewNo: newNo})
			newNo++
		case '-':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: '-', Text: lineText, OldNo: oldNo, NewNo: newNo})
			oldNo++
		default:
			hunk = nil
		}
	}
	finishFile()
	return files
}
