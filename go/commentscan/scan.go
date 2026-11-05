// SPDX-License-Identifier: MIT OR Apache-2.0

package commentscan

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RigelBuild/meissa/go/internal/jstext"
)

// LanguageID identifies a supported scanner language.
type LanguageID string

// Residues contains one code residue per input line and the lexical ambiguity state.
type Residues struct {
	Residues  []string
	Ambiguous bool
}

type frameKind uint8

const (
	frameBlock frameKind = iota
	frameString
	frameInterpolation
	frameIndent
	frameHeredoc
)

type scanFrame struct {
	kind         frameKind
	depth        int
	keep         bool
	spec         *StringSpec
	closingToken string
	brace        int
	openChar     string
	lang         *LangConfig
	indent       int
	parent       int
	unknown      bool
	delimiter    string
	stripTabs    bool
}
type scanState struct{ stack []scanFrame }
type lineScan struct {
	state     scanState
	residue   string
	underflow bool
	malformed bool
}
type completeScan struct {
	residues  []string
	underflow bool
	unclosed  bool
	malformed bool
}
type openedString struct {
	spec *StringSpec
	text string
}

var (
	trailingWS   = regexp.MustCompile(`[ \t]+$`)
	keywordRegex = regexp.MustCompile(`(return|throw|case|delete|void|typeof|instanceof|in|of)` + jsSpace + `*$`)
)

func isRegexPreceder(value byte) bool {
	switch value {
	case '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', ';', '+', '-', '*', '%', '<', '>', '^', '~':
		return true
	default:
		return false
	}
}

func hasPrefixAt(text string, at int, prefix string) bool {
	return at >= 0 && at <= len(text) && strings.HasPrefix(text[at:], prefix)
}
func matchAt(re *regexp.Regexp, text string, at int) []string {
	return re.FindStringSubmatch(text[at:])
}
func indentOf(text string) int {
	for i := range len(text) {
		if text[i] != ' ' && text[i] != '\t' {
			return i
		}
	}
	return len(text)
}
func effectiveLang(lang *LangConfig, stack []scanFrame) *LangConfig {
	for i := range slices.Backward(stack) {
		f := stack[i]
		if f.kind == frameInterpolation && f.lang != nil {
			return f.lang
		}
	}
	return lang
}
func startsLineComment(lang *LangConfig, text string, at int) bool {
	return lang.LineCommentAfter == nil || at == 0 || lang.LineCommentAfter.MatchString(text[at-1:at])
}
func rustCharLen(text string, at int) int {
	if at+1 >= len(text) {
		return 0
	}
	if text[at+1] == '\\' {
		for i := at + 2; i < len(text); i++ {
			if text[i] == '\'' {
				return i - at + 1
			}
		}
		return 0
	}
	_, size := utf8.DecodeRuneInString(text[at+1:])
	if size > 0 && at+1+size < len(text) && text[at+1+size] == '\'' {
		return size + 2
	}
	return 0
}
func regexLiteralLen(text string, at int) int {
	j, inClass := at+1, false
	if j >= len(text) || text[j] == '/' || text[j] == '*' {
		return 0
	}
	for j < len(text) {
		c := text[j]
		if c == '\\' {
			j += 2
			continue
		}
		switch {
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			j++
			for j < len(text) && text[j] >= 'a' && text[j] <= 'z' {
				j++
			}
			return j - at
		}
		j++
	}
	return 0
}

func openString(lang *LangConfig, text string, at int, residue string) *openedString {
	for i := range lang.Strings {
		spec := &lang.Strings[i]
		open := ""
		matched := false
		if spec.OpenRe != nil {
			if lang.ID == "css" && spec.Open == "url(" {
				open, matched = cssURLOpen(text[at:])
			} else if captures := matchAt(spec.OpenRe, text, at); captures != nil {
				open, matched = captures[0], true
			}
		} else if hasPrefixAt(text, at, spec.Open) {
			open, matched = spec.Open, true
		}
		if !matched {
			continue
		}
		if spec.OpenAfter != nil && !spec.OpenAfter.MatchString(jstext.TrimRight(residue)) {
			continue
		}
		return &openedString{spec: spec, text: open}
	}
	return nil
}

// newFrame returns a scanner frame of kind with every optional field zeroed.
func newFrame(kind frameKind) scanFrame {
	return scanFrame{
		kind: kind, depth: 0, keep: false, spec: nil, closingToken: "", brace: 0, openChar: "",
		lang: nil, indent: 0, parent: 0, unknown: false, delimiter: "", stripTabs: false,
	}
}

// lineScanner holds the mutable state of one scanLine pass.
type lineScanner struct {
	lang      *LangConfig
	active    *LangConfig
	text      string
	stack     []scanFrame
	pending   []scanFrame
	residue   strings.Builder
	underflow bool
}

func scanLine(lang *LangConfig, entry scanState, text string) lineScan {
	stack := append([]scanFrame(nil), entry.stack...)
	if top := topFrame(stack); top != nil {
		switch top.kind {
		case frameHeredoc:
			if heredocEnds(top, text) {
				stack = stack[:len(stack)-1]
			}
			return passLine(stack, text)
		case frameIndent:
			if indentContinues(top, text) {
				return passLine(stack, text)
			}
			return scanLine(lang, scanState{stack: stack[:len(stack)-1]}, text)
		case frameBlock, frameString, frameInterpolation:
		}
	}
	var s lineScanner
	s.lang, s.active, s.text, s.stack = lang, effectiveLang(lang, stack), text, stack
	for i := 0; i < len(text); {
		next, done := s.step(i)
		if done {
			break
		}
		i = next
	}
	malformed := false
	if top := topFrame(s.stack); top != nil && top.kind == frameString && !top.spec.Multiline {
		s.pop()
		malformed = true
	}
	for i := range slices.Backward(s.pending) {
		s.stack = append(s.stack, s.pending[i])
	}
	s.pushIndentScalar()
	return lineScan{
		state: scanState{stack: s.stack}, residue: trimTrailing(s.residue.String()),
		underflow: s.underflow, malformed: malformed,
	}
}

// passLine keeps a heredoc or block-scalar body line as code.
func passLine(stack []scanFrame, text string) lineScan {
	return lineScan{state: scanState{stack: stack}, residue: trimTrailing(text), underflow: false, malformed: false}
}

// heredocEnds reports whether text is the heredoc terminator.
func heredocEnds(top *scanFrame, text string) bool {
	if top.stripTabs {
		text = strings.TrimLeft(text, "\t")
	}
	return text == top.delimiter
}

// indentContinues reports whether text stays inside the block scalar, fixing an unknown indent on first use.
func indentContinues(top *scanFrame, text string) bool {
	if jstext.Trim(text) == "" {
		return true
	}
	indent := indentOf(text)
	if !top.unknown {
		return indent >= top.indent
	}
	if indent <= top.parent {
		return false
	}
	top.indent = indent
	top.unknown = false
	return true
}

func (s *lineScanner) pop() { s.stack = s.stack[:len(s.stack)-1] }

// emit copies text[from:to] to the residue and returns to.
func (s *lineScanner) emit(from, to int) int {
	s.residue.WriteString(s.text[from:to])
	return to
}

// step consumes one token at i and returns the next index; done means a line comment ended the line.
func (s *lineScanner) step(i int) (int, bool) {
	if top := topFrame(s.stack); top != nil {
		switch top.kind {
		case frameBlock:
			return s.stepBlock(top, i), false
		case frameString:
			return s.stepString(top, i), false
		case frameInterpolation:
			if next, ok := s.stepInterpolation(top, i); ok {
				return next, false
			}
		case frameIndent, frameHeredoc:
		}
	}
	return s.stepCode(i)
}

func (s *lineScanner) stepBlock(top *scanFrame, i int) int {
	open, closingToken := blockTokens(s.active)
	if s.active.NestBlock && hasPrefixAt(s.text, i, open) {
		top.depth++
		if top.keep {
			s.residue.WriteString(open)
		}
		return i + len(open)
	}
	if hasPrefixAt(s.text, i, closingToken) {
		top.depth--
		if top.keep {
			s.residue.WriteString(closingToken)
		}
		if top.depth == 0 {
			s.pop()
		}
		return i + len(closingToken)
	}
	_, size := utf8.DecodeRuneInString(s.text[i:])
	if top.keep {
		s.residue.WriteString(s.text[i : i+size])
	}
	return i + size
}

func (s *lineScanner) stepString(top *scanFrame, i int) int {
	spec, text, closingToken := top.spec, s.text, top.closingToken
	switch {
	case spec.Escape == EscapeBackslash && text[i] == '\\':
		return s.emit(i, min(i+2, len(text)))
	case spec.Escape == EscapeDoubled && hasPrefixAt(text, i, closingToken) &&
		hasPrefixAt(text, i+len(closingToken), closingToken):
		return s.emit(i, i+2*len(closingToken))
	case spec.Escape == EscapeNixIndented && hasPrefixAt(text, i, "''"):
		return s.stepNixIndented(closingToken, i)
	}
	if spec.Interpolation {
		if next, ok := s.openInterpolation(spec, i); ok {
			return next
		}
	}
	if spec.Escape != EscapeNixIndented && hasPrefixAt(text, i, closingToken) {
		s.residue.WriteString(closingToken)
		s.pop()
		s.active = effectiveLang(s.lang, s.stack)
		return i + len(closingToken)
	}
	_, size := utf8.DecodeRuneInString(text[i:])
	return s.emit(i, i+size)
}

// stepNixIndented handles a `”` inside a Nix indented string: an escape or the closing delimiter.
func (s *lineScanner) stepNixIndented(closingToken string, i int) int {
	switch {
	case hasPrefixAt(s.text, i, "''${"):
		return s.emit(i, i+4)
	case hasPrefixAt(s.text, i, "'''"):
		return s.emit(i, i+3)
	case hasPrefixAt(s.text, i, "''\\"):
		return s.emit(i, min(i+4, len(s.text)))
	default:
		s.residue.WriteString(closingToken)
		s.pop()
		return i + len(closingToken)
	}
}

func (s *lineScanner) openInterpolation(spec *StringSpec, i int) (int, bool) {
	open := cmp.Or(spec.InterpOpen, "${")
	if !hasPrefixAt(s.text, i, open) {
		return i, false
	}
	s.residue.WriteString(open)
	frame := newFrame(frameInterpolation)
	frame.closingToken = cmp.Or(spec.InterpClose, "}")
	frame.openChar = open[len(open)-1:]
	frame.lang = spec.InterpLang
	s.stack = append(s.stack, frame)
	if spec.InterpLang != nil {
		s.active = spec.InterpLang
	}
	return i + len(open), true
}

func (s *lineScanner) stepInterpolation(top *scanFrame, i int) (int, bool) {
	if hasPrefixAt(s.text, i, top.closingToken) {
		s.residue.WriteString(top.closingToken)
		next := i + len(top.closingToken)
		if top.brace == 0 {
			s.pop()
			s.active = effectiveLang(s.lang, s.stack)
		} else {
			top.brace--
		}
		return next, true
	}
	if hasPrefixAt(s.text, i, top.openChar) {
		top.brace++
		s.residue.WriteString(top.openChar)
		return i + len(top.openChar), true
	}
	return i, false
}

func (s *lineScanner) stepCode(i int) (int, bool) {
	text, active := s.text, s.active
	if s.lineCommentAt(i) {
		if active.Directive.MatchString(text[i:]) || active.Directive.MatchString(jstext.TrimLeft(text)) {
			s.residue.WriteString(text[i:])
		}
		return len(text), true
	}
	blockOpen, blockClose := blockTokens(active)
	if active.BlockComment && hasPrefixAt(text, i, blockOpen) {
		frame := newFrame(frameBlock)
		frame.depth = 1
		frame.keep = active.Directive.MatchString(text[i:])
		s.stack = append(s.stack, frame)
		if frame.keep {
			s.residue.WriteString(blockOpen)
		}
		return i + len(blockOpen), false
	}
	if active.BlockComment && hasPrefixAt(text, i, blockClose) {
		s.underflow = true
		return i + len(blockClose), false
	}
	if size := s.regexLiteralAt(i); size > 0 {
		return s.emit(i, i+size), false
	}
	if active.RustChar && text[i] == '\'' {
		if size := rustCharLen(text, i); size > 0 {
			return s.emit(i, i+size), false
		}
	}
	if next, ok := s.openHeredoc(i); ok {
		return next, false
	}
	if expression := active.Expr; expression != nil && hasPrefixAt(text, i, expression.Open) {
		s.residue.WriteString(expression.Open)
		frame := newFrame(frameInterpolation)
		frame.closingToken = expression.Close
		frame.openChar = expression.Open[len(expression.Open)-1:]
		frame.lang = expression.Lang
		s.stack = append(s.stack, frame)
		s.active = expression.Lang
		return i + len(expression.Open), false
	}
	if opened := openString(active, text, i, s.residue.String()); opened != nil {
		s.residue.WriteString(opened.text)
		frame := newFrame(frameString)
		frame.spec = opened.spec
		frame.closingToken = opened.spec.Close
		if opened.spec.CloseIsOpen {
			frame.closingToken = opened.text
		}
		s.stack = append(s.stack, frame)
		return i + len(opened.text), false
	}
	_, size := utf8.DecodeRuneInString(text[i:])
	return s.emit(i, i+size), false
}

func (s *lineScanner) lineCommentAt(i int) bool {
	for _, token := range s.active.LineComment {
		if hasPrefixAt(s.text, i, token) {
			return startsLineComment(s.active, s.text, i)
		}
	}
	return false
}

// regexLiteralAt returns the length of a JS regex literal at i, or 0 when `/` is division or absent.
func (s *lineScanner) regexLiteralAt(i int) int {
	if !s.active.RegexLiteral || s.text[i] != '/' {
		return 0
	}
	trimmed := jstext.TrimRight(s.residue.String())
	if trimmed != "" && !isRegexPreceder(trimmed[len(trimmed)-1]) && !keywordRegex.MatchString(trimmed) {
		return 0
	}
	return regexLiteralLen(s.text, i)
}

// openHeredoc queues a heredoc that opens at i; its body starts on the next line.
func (s *lineScanner) openHeredoc(i int) (int, bool) {
	if s.active.Heredoc == nil || s.text[i] != '<' || len(s.stack) != 0 {
		return i, false
	}
	captures := matchAt(s.active.Heredoc, s.text, i)
	if captures == nil {
		return i, false
	}
	delimiter := cmp.Or(captures[1:]...)
	if delimiter == "" {
		return i, false
	}
	frame := newFrame(frameHeredoc)
	frame.delimiter = delimiter
	frame.stripTabs = hasPrefixAt(s.text, i, "<<-")
	s.pending = append(s.pending, frame)
	s.residue.WriteString(captures[0])
	return i + len(captures[0]), true
}

// pushIndentScalar opens a YAML block scalar when the line's residue ends in an indicator.
func (s *lineScanner) pushIndentScalar() {
	if s.lang.IndentScalar == nil || len(s.stack) != 0 {
		return
	}
	captures := s.lang.IndentScalar.FindStringSubmatch(s.residue.String())
	if captures == nil {
		return
	}
	frame := newFrame(frameIndent)
	frame.parent = indentOf(s.text)
	digit := ""
	if len(captures) > 2 {
		digit = captures[2]
	}
	frame.unknown = digit == ""
	if digit != "" {
		frame.indent = frame.parent + int(digit[0]-'0')
	}
	s.stack = append(s.stack, frame)
}

func topFrame(stack []scanFrame) *scanFrame {
	if len(stack) == 0 {
		return nil
	}
	return &stack[len(stack)-1]
}
func blockTokens(lang *LangConfig) (string, string) {
	open, closingToken := lang.BlockOpen, lang.BlockClose
	if open == "" {
		open = "/*"
	}
	if closingToken == "" {
		closingToken = "*/"
	}
	return open, closingToken
}
func trimTrailing(text string) string { return trailingWS.ReplaceAllString(text, "") }
func scanComplete(lang *LangConfig, text string) completeScan {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	residues := make([]string, 0, len(lines))
	state := scanState{stack: []scanFrame{}}
	underflow, malformed := false, false
	for _, line := range lines {
		scan := scanLine(lang, state, line)
		state = scan.state
		residues = append(residues, scan.residue)
		underflow = underflow || scan.underflow
		malformed = malformed || scan.malformed
	}
	unclosed := false
	for _, frame := range state.stack {
		if frame.kind != frameIndent {
			unclosed = true
			break
		}
	}
	return completeScan{residues: residues, underflow: underflow, unclosed: unclosed, malformed: malformed}
}

var errUnknownLanguage = errors.New("no scanner for language")

// ScanText scans one complete source document and returns one code residue per line.
func ScanText(lang LanguageID, text string) (Residues, error) {
	if lang == LanguageTSX {
		return ScanJSX(text), nil
	}
	if lang == LanguageAstro {
		return ScanAstro(text), nil
	}
	config := langByID()[lang]
	if config == nil {
		return Residues{}, fmt.Errorf("%w: %s", errUnknownLanguage, lang)
	}
	scan := scanComplete(config, text)
	return Residues{Residues: scan.residues, Ambiguous: scan.underflow || scan.unclosed || scan.malformed}, nil
}

// ResidueScannerFor selects a scanner by file extension.
func ResidueScannerFor(ext string) (func(text string) Residues, bool) {
	switch ext {
	case ".tsx", ".jsx":
		return ScanJSX, true
	case ".astro":
		return ScanAstro, true
	}
	config := langByExt()[ext]
	if config == nil {
		return nil, false
	}
	return func(text string) Residues {
		scan := scanComplete(config, text)
		return Residues{Residues: scan.residues, Ambiguous: scan.underflow || scan.unclosed || scan.malformed}
	}, true
}
