// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// CommentBlock describes a contiguous run of comment-only lines.
type CommentBlock struct {
	Start int
	Lines int
	First string
	Doc   bool
}

// BlockScan contains comment-only runs and the scanner ambiguity state.
type BlockScan struct {
	Blocks    []CommentBlock
	Ambiguous bool
}

type goTokenKind uint8

const (
	goIdentifier goTokenKind = iota
	goKeyword
	goLiteral
	goString
	goSymbol
)

type goToken struct {
	kind       goTokenKind
	value      string
	line       int
	braceDepth int
}
type goAnalysisData struct {
	docByLine  map[int]bool
	specByLine map[int]bool
}
type protoScope string

const (
	protoMessage protoScope = "message"
	protoService protoScope = "service"
	protoEnum    protoScope = "enum"
	protoOther   protoScope = "other"
)

type commentRun struct {
	start int
	count int
}

func isGoKeyword(value string) bool {
	switch value {
	case "break", "case", "chan", "const", "continue", "default", "defer", "else",
		"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var":
		return true
	default:
		return false
	}
}

func isGoSemicolonKeyword(value string) bool {
	switch value {
	case "break", "continue", "fallthrough", "return":
		return true
	default:
		return false
	}
}

// The proto patterns use jsSpace and jsNonSpace so Unicode spaces split tokens as in JavaScript.
var (
	generatedMarker = regexp.MustCompile(`Code generated\b.*DO NOT EDIT\.|@generated\b`)
	protoToken      = regexp.MustCompile(`"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[\w.]+|` + jsNonSpace)
	protoDecl       = regexp.MustCompile(`^(message|enum|service)(?:` + jsSpace + `|$)`)
	protoRPC        = regexp.MustCompile(`^rpc` + jsSpace + `+[\w]+` + jsSpace + `*\(`)
	protoEnumValue  = regexp.MustCompile(`^[\w]+` + jsSpace + `*=` + jsSpace + `*-?\d+\b`)
	protoField      = regexp.MustCompile(
		`^(?:(optional|required|repeated)` + jsSpace + `+)?(?:map` + jsSpace + `*<[^>]+>|[\w.]+)` +
			jsSpace + `+[\w]+` + jsSpace + `*=` + jsSpace + `*\d+\b`,
	)
	protoBlock      = regexp.MustCompile(`/\*(?s:.)*?\*/`)
	protoIdentifier = regexp.MustCompile(`^[\w.]+$`)
)

func canInsertSemicolon(token *goToken) bool {
	if token == nil {
		return false
	}
	if token.kind == goIdentifier || token.kind == goString || token.kind == goLiteral {
		return true
	}
	if token.kind == goKeyword {
		return isGoSemicolonKeyword(token.value)
	}
	switch token.value {
	case ")", "]", "}", "++", "--":
		return true
	}
	return false
}

func goTokens(source string) []goToken {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	tokens := make([]goToken, 0)
	line, braceDepth := 0, 0
	var previous *goToken
	push := func(kind goTokenKind, value string, tokenLine int) {
		depth := braceDepth
		if value == "}" && depth > 0 {
			depth--
		}
		tokens = append(tokens, goToken{kind: kind, value: value, line: tokenLine, braceDepth: depth})
		previous = &tokens[len(tokens)-1]
		if value == "{" {
			braceDepth++
		}
		if value == "}" && braceDepth > 0 {
			braceDepth--
		}
	}
	for i := 0; i < len(source); {
		char := source[i]
		if char == '\n' {
			if canInsertSemicolon(previous) {
				push(goSymbol, ";", line)
			}
			line++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(source[i:])
		if jstext.IsSpace(r) {
			i += size
			continue
		}
		if char == '/' && i+1 < len(source) && source[i+1] == '/' {
			if end := strings.IndexByte(source[i:], '\n'); end < 0 {
				i = len(source)
			} else {
				i += end
			}
			continue
		}
		if char == '/' && i+1 < len(source) && source[i+1] == '*' {
			i += 2
			for i < len(source) && (source[i] != '*' || i+1 >= len(source) || source[i+1] != '/') {
				if source[i] == '\n' {
					if canInsertSemicolon(previous) {
						push(goSymbol, ";", line)
					}
					line++
				}
				i++
			}
			if i < len(source) {
				i += 2
			}
			continue
		}
		if char == '`' {
			start, startLine := i, line
			i++
			for i < len(source) && source[i] != '`' {
				if source[i] == '\n' {
					line++
				}
				i++
			}
			if i < len(source) {
				i++
			}
			push(goString, source[start:i], startLine)
			continue
		}
		if char == '"' || char == '\'' {
			start, startLine, quote := i, line, char
			i++
			escaped := false
		scanQuoted:
			for i < len(source) && source[i] != '\n' {
				current := source[i]
				i++
				switch {
				case escaped:
					escaped = false
				case current == '\\':
					escaped = true
				case current == quote:
					break scanQuoted
				}
			}
			kind := goString
			if quote == '\'' {
				kind = goLiteral
			}
			push(kind, source[start:i], startLine)
			continue
		}
		if char >= '0' && char <= '9' {
			start := i
			i += size
			for i < len(source) {
				next, n := utf8.DecodeRuneInString(source[i:])
				if !unicode.IsLetter(next) && !unicode.IsNumber(next) && next != '_' && next != '.' {
					break
				}
				i += n
			}
			push(goLiteral, source[start:i], line)
			continue
		}
		if r == '_' || unicode.IsLetter(r) {
			start := i
			i += size
			for i < len(source) {
				next, n := utf8.DecodeRuneInString(source[i:])
				if next != '_' && !unicode.IsLetter(next) && !unicode.IsNumber(next) {
					break
				}
				i += n
			}
			value := source[start:i]
			kind := goIdentifier
			if isGoKeyword(value) {
				kind = goKeyword
			}
			push(kind, value, line)
			continue
		}
		if i+1 < len(source) && (source[i:i+2] == "++" || source[i:i+2] == "--") {
			push(goSymbol, source[i:i+2], line)
			i += 2
			continue
		}
		push(goSymbol, source[i:i+size], line)
		i += size
	}
	return tokens
}

func exportedName(name string) bool { r, _ := utf8.DecodeRuneInString(name); return unicode.IsUpper(r) }
func namesExported(tokens []goToken) bool {
	if len(tokens) == 0 || tokens[0].kind != goIdentifier {
		return false
	}
	if exportedName(tokens[0].value) {
		return true
	}
	for i := 1; i < len(tokens) && tokens[i].value == ","; i += 2 {
		if i+1 >= len(tokens) || tokens[i+1].kind != goIdentifier {
			return false
		}
		if exportedName(tokens[i+1].value) {
			return true
		}
	}
	return false
}

func goAnalysis(tokens []goToken) goAnalysisData {
	result := goAnalysisData{docByLine: map[int]bool{}, specByLine: map[int]bool{}}
	record := func(line int, doc bool) {
		if _, ok := result.docByLine[line]; !ok {
			result.docByLine[line] = doc
		}
	}
	for i := 0; i < len(tokens); i++ {
		keyword := tokens[i]
		if keyword.kind != goKeyword {
			continue
		}
		if keyword.value == "package" {
			if keyword.braceDepth == 0 && i+1 < len(tokens) && tokens[i+1].kind == goIdentifier {
				record(keyword.line, true)
			}
			continue
		}
		if keyword.value == "func" {
			name := funcNameIndex(tokens, i)
			if keyword.braceDepth == 0 && name < len(tokens) && tokens[name].kind == goIdentifier {
				record(keyword.line, exportedName(tokens[name].value))
			}
			continue
		}
		if keyword.value != "const" && keyword.value != "type" && keyword.value != "var" {
			continue
		}
		if keyword.braceDepth != 0 {
			record(keyword.line, false)
			continue
		}
		if i+1 >= len(tokens) || tokens[i+1].value != "(" {
			spec := make([]goToken, 0)
			parens, brackets, braces := 0, 0, 0
			for j := i + 1; j < len(tokens); j++ {
				token := tokens[j]
				if token.value == ";" && parens == 0 && brackets == 0 && braces == 0 {
					break
				}
				spec = append(spec, token)
				switch token.value {
				case "(":
					parens++
				case ")":
					parens--
				case "[":
					brackets++
				case "]":
					brackets--
				case "{":
					braces++
				case "}":
					braces--
				}
			}
			record(keyword.line, namesExported(spec))
			continue
		}
		parenDepth, braceDepth, closeIndex := 1, 0, i+1
		segment := make([]goToken, 0)
		groupExported := false
		finishSpec := func() {
			if len(segment) > 0 && segment[0].kind == goIdentifier {
				exported := namesExported(segment)
				if _, ok := result.specByLine[segment[0].line]; !ok {
					result.specByLine[segment[0].line] = exported
				}
				groupExported = groupExported || exported
			}
			segment = segment[:0]
		}
		for j := i + 2; j < len(tokens); j++ {
			token := tokens[j]
			if token.value == ")" && parenDepth == 1 && braceDepth == 0 {
				finishSpec()
				closeIndex = j
				break
			}
			if token.value == ";" && parenDepth == 1 && braceDepth == 0 {
				finishSpec()
				continue
			}
			segment = append(segment, token)
			if token.value == "(" {
				parenDepth++
			}
			if token.value == ")" {
				parenDepth--
			}
			if token.value == "{" {
				braceDepth++
			}
			if token.value == "}" {
				braceDepth--
			}
		}
		record(keyword.line, groupExported)
		i = closeIndex
	}
	return result
}

// funcNameIndex returns the index of the name after the func keyword at i, skipping a receiver list.
func funcNameIndex(tokens []goToken, i int) int {
	name := i + 1
	if name >= len(tokens) || tokens[name].value != "(" {
		return name
	}
	depth := 1
	name++
	for name < len(tokens) && depth > 0 {
		if tokens[name].value == "(" {
			depth++
		}
		if tokens[name].value == ")" {
			depth--
		}
		name++
	}
	return name
}
func goDocAt(analysis goAnalysisData, line int) bool {
	if value, ok := analysis.docByLine[line]; ok {
		return value
	}
	return analysis.specByLine[line]
}

func protoScopeText(text string) []string {
	var out strings.Builder
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		char := text[i]
		if quote != 0 {
			out.WriteByte(char)
			if char == '\\' && i+1 < len(text) {
				i++
				out.WriteByte(text[i])
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' {
			quote = char
			out.WriteByte(char)
			continue
		}
		if char == '/' && i+1 < len(text) && text[i+1] == '/' {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
			continue
		}
		if char == '/' && i+1 < len(text) && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "*/")
			stop := len(text)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			out.WriteByte(' ')
			for _, r := range text[i:stop] {
				if r == '\n' {
					out.WriteByte('\n')
				}
			}
			i = stop - 1
			continue
		}
		out.WriteByte(char)
	}
	return strings.Split(out.String(), "\n")
}
func protoScopes(lines []string) [][]protoScope {
	scopes := make([][]protoScope, 0, len(lines))
	stack := make([]protoScope, 0)
	prev2, prev1 := "", ""
	for _, line := range lines {
		scopes = append(scopes, append([]protoScope(nil), stack...))
		for _, token := range protoToken.FindAllString(line, -1) {
			if token == "}" && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if token == "{" {
				kind := protoOther
				if protoIdentifier.MatchString(prev1) {
					switch prev2 {
					case "message":
						kind = protoMessage
					case "service":
						kind = protoService
					case "enum":
						kind = protoEnum
					}
				}
				stack = append(stack, kind)
			}
			prev2, prev1 = prev1, token
		}
	}
	return scopes
}
func protoDocAt(line string, scopes []protoScope) bool {
	text := jstext.Trim(protoBlock.ReplaceAllString(line, " "))
	if protoDecl.MatchString(text) {
		return true
	}
	for _, scope := range scopes {
		if scope == protoService && protoRPC.MatchString(text) {
			return true
		}
		if scope == protoEnum && protoEnumValue.MatchString(text) {
			return true
		}
		if scope == protoMessage && protoField.MatchString(text) {
			return true
		}
	}
	return false
}
func docMarker(first string) bool {
	text := jstext.TrimLeft(first)
	return strings.HasPrefix(text, "/**") || strings.HasPrefix(text, "/*!") ||
		strings.HasPrefix(text, "///") || strings.HasPrefix(text, "//!")
}
func generatedMarkerInComment(line, residue string, directive *regexp.Regexp) bool {
	var comment strings.Builder
	residueIndex := 0
	for _, char := range line {
		if residueIndex < len(residue) {
			residueRune, size := utf8.DecodeRuneInString(residue[residueIndex:])
			if char == residueRune {
				residueIndex += size
				continue
			}
		}
		comment.WriteRune(char)
	}
	commentMarker := generatedMarker.MatchString(comment.String())
	directiveMarker := directive != nil && directive.MatchString(jstext.TrimLeft(line)) &&
		generatedMarker.MatchString(line)
	return commentMarker || directiveMarker
}
func commentRuns(lines, residues []string) []commentRun {
	runs := make([]commentRun, 0)
	for i := 0; i < len(lines); {
		if jstext.Trim(lines[i]) == "" || jstext.Trim(residues[i]) != "" {
			i++
			continue
		}
		start := i
		for i < len(lines) && jstext.Trim(lines[i]) != "" && jstext.Trim(residues[i]) == "" {
			i++
		}
		runs = append(runs, commentRun{start, i - start})
	}
	return runs
}

// CommentBlocks groups comment-only lines and classifies documentation blocks.
func CommentBlocks(text, ext string) (BlockScan, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	scanner, ok := ResidueScannerFor(ext)
	if !ok {
		return BlockScan{Blocks: []CommentBlock{}, Ambiguous: false}, false
	}
	var tokens []goToken
	if ext == ".go" {
		tokens = goTokens(text)
		if isCgoTokens(tokens) {
			return BlockScan{Blocks: []CommentBlock{}, Ambiguous: true}, true
		}
	}
	scan := scanner(text)
	if scan.Ambiguous {
		return BlockScan{Blocks: []CommentBlock{}, Ambiguous: true}, true
	}
	lines := strings.Split(text, "\n")
	generated := false
	for i, line := range lines {
		if i == 5 {
			break
		}
		var directive *regexp.Regexp
		if config := langByExt()[ext]; config != nil {
			directive = config.Directive
		}
		if generatedMarkerInComment(line, scan.Residues[i], directive) {
			generated = true
			break
		}
	}
	analysis := goAnalysisData{docByLine: map[int]bool{}, specByLine: map[int]bool{}}
	if ext == ".go" {
		analysis = goAnalysis(tokens)
	}
	var scopes [][]protoScope
	if ext == ".proto" {
		scopes = protoScopes(protoScopeText(text))
	}
	runs := commentRuns(lines, scan.Residues)
	blocks := make([]CommentBlock, 0, len(runs))
	for _, run := range runs {
		first := lines[run.start]
		next := run.start + run.count
		attached := next < len(lines) && jstext.Trim(lines[next]) != "" && jstext.Trim(scan.Residues[next]) != ""
		doc := docMarker(first)
		if attached && ext == ".go" {
			doc = doc || goDocAt(analysis, next)
		}
		if attached && ext == ".proto" && next < len(scopes) {
			doc = doc || protoDocAt(lines[next], scopes[next])
		}
		blocks = append(blocks, CommentBlock{Start: run.start + 1, Lines: run.count, First: first, Doc: generated || doc})
	}
	return BlockScan{Blocks: blocks, Ambiguous: false}, true
}

// IsCgo reports whether Go source imports the special C pseudo-package.
func IsCgo(text string) bool { return isCgoTokens(goTokens(text)) }
func isCgoTokens(tokens []goToken) bool {
	for i := range tokens {
		if tokens[i].value != "import" {
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].kind == goString && tokens[i+1].value == `"C"` {
			return true
		}
		if i+1 >= len(tokens) || tokens[i+1].value != "(" {
			continue
		}
		depth := 1
		for j := i + 2; j < len(tokens); j++ {
			token := tokens[j]
			if token.value == "(" {
				depth++
			}
			if token.value == ")" {
				depth--
				if depth == 0 {
					break
				}
			}
			if depth == 1 && token.kind == goString && token.value == `"C"` {
				return true
			}
		}
	}
	return false
}
