// SPDX-License-Identifier: MIT OR Apache-2.0

// Package commentscan scans source text into code residues and comment blocks.
package commentscan

import (
	"regexp"
	"strings"
)

// EscapeMode describes how an escaped delimiter is recognized.
type EscapeMode string

// Escape modes for string-like regions.
const (
	EscapeBackslash   EscapeMode = "backslash"
	EscapeNixIndented EscapeMode = "nix-indented"
	EscapeDoubled     EscapeMode = "doubled"
	EscapeNone        EscapeMode = "none"
)

// StringSpec describes a string-like lexical region.
type StringSpec struct {
	Open, Close             string
	Escape                  EscapeMode
	Multiline               bool
	Interpolation           bool
	InterpOpen, InterpClose string
	InterpLang              *LangConfig
	CloseIsOpen             bool
	OpenRe, OpenAfter       *regexp.Regexp
}

// ExprSpec describes an embedded expression region.
type ExprSpec struct {
	Open, Close string
	Lang        *LangConfig
}

// LangConfig describes comment and string syntax for a source language.
type LangConfig struct {
	ID                    LanguageID
	LineComment           []string
	LineCommentAfter      *regexp.Regexp
	BlockComment          bool
	BlockOpen, BlockClose string
	NestBlock             bool
	RustChar              bool
	RegexLiteral          bool
	Strings               []StringSpec
	Expr                  *ExprSpec
	IndentScalar          *regexp.Regexp
	Heredoc               *regexp.Regexp
	Directive             *regexp.Regexp
}

// Supported scanner language IDs.
const (
	LanguageGo     LanguageID = "go"
	LanguageTS     LanguageID = "ts"
	LanguageRust   LanguageID = "rust"
	LanguageNix    LanguageID = "nix"
	LanguagePython LanguageID = "python"
	LanguageTOML   LanguageID = "toml"
	LanguageYAML   LanguageID = "yaml"
	LanguageShell  LanguageID = "shell"
	LanguageSQL    LanguageID = "sql"
	LanguageKDL    LanguageID = "kdl"
	LanguageCSS    LanguageID = "css"
	LanguageHTML   LanguageID = "html"
	LanguageTSX    LanguageID = "tsx"
	LanguageAstro  LanguageID = "astro"
	LanguageProto  LanguageID = "proto"
)

// jsSpaceChars are the ECMAScript `\s` characters; Go's `\s` is ASCII-only.
const jsSpaceChars = `	\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsSpace and jsNonSpace are the RE2 forms of ECMAScript `\s` and `\S`.
const (
	jsSpace    = `[` + jsSpaceChars + `]`
	jsNonSpace = `[^` + jsSpaceChars + `]`
)

// A language with no tool-read comment family answers with a regex that cannot match.
var never = regexp.MustCompile(`$^`)

var goDirective = regexp.MustCompile(
	`^//` + jsSpace + `*(go:|nolint|lint:|noinspection|export` + jsSpace + `|cgo` + jsSpace + `)`,
)

var tsDirective = regexp.MustCompile(
	`^(?://[/!]?|/\*)` + jsSpace + `*(@ts-|<reference|biome-ignore|eslint|prettier-ignore|istanbul` +
		jsSpace + `|c8` + jsSpace + `|v8` + jsSpace + `|@jsx|@license|@preserve|webpack|@vite-)`,
)

var rustDirective = regexp.MustCompile(`^//[/!]?` + jsSpace + `*(rustfmt::|clippy::|@generated|cargo:)`)

var nixDirective = regexp.MustCompile(
	`^#!|^#` + jsSpace + `*(nix:|shellcheck` + jsSpace + `|nixfmt:|deadnix:)`,
)

var pythonDirective = regexp.MustCompile(
	`^#!|^#` + jsSpace + `*(type:|noqa|pylint:|mypy:|ruff:|fmt:|-\*-` + jsSpace + `*coding)`,
)

var yamlValueStart = regexp.MustCompile(`(^|[:\-\[{,?])$`)

var yamlCommentAfter = regexp.MustCompile(`[ \t]`)

// indentScalar matches a block-scalar indicator; group 2 is the indentation indicator.
var indentScalar = regexp.MustCompile(
	`(^|:|-)[ \t]*(?:(?:!!?[\w/.-]*|&[\w-]+|\*[\w-]+)[ \t]+)*[|>][-+]?([0-9]?)[-+]?[ \t]*(#.*)?$`,
)

var yamlDirective = regexp.MustCompile(
	`^#` + jsSpace + `*(yaml-language-server:|yamllint` + jsSpace +
		`|renovate:|prettier-ignore|GENERATED|DO NOT EDIT)|^\{\{`,
)

var shellCommentAfter = regexp.MustCompile(`[ \t;&|(]`)

var shellHeredoc = regexp.MustCompile(
	`^<<-?[ \t]*(?:\\([A-Za-z_]\w*)|'([A-Za-z_]\w*)'|"([A-Za-z_]\w*)"|([A-Za-z_]\w*))`,
)

var shellDirective = regexp.MustCompile(`^#!|^#` + jsSpace + `*shellcheck` + jsSpace)

var sqlTagOpen = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*\$`)

var sqlDirective = regexp.MustCompile(`^--` + jsSpace + `*name:`)

var cssURLOpenRe = regexp.MustCompile(`(?i)^url\(`)

var cssDirective = regexp.MustCompile(`^/\*` + jsSpace + `*(stylelint-|biome-ignore|postcss-)`)

var attrValueStart = regexp.MustCompile(`=$`)

var htmlDirective = regexp.MustCompile(
	`^<!--` + jsSpace + `*(prettier-ignore|eslint|biome-ignore|markdownlint|rumdl|htmlhint|\[if\b)`,
)

// newStringSpec returns a plain string region; callers set the optional fields they need.
func newStringSpec(open, closeToken string, escape EscapeMode, multiline bool) StringSpec {
	return StringSpec{
		Open:          open,
		Close:         closeToken,
		Escape:        escape,
		Multiline:     multiline,
		Interpolation: false,
		InterpOpen:    "",
		InterpClose:   "",
		InterpLang:    nil,
		CloseIsOpen:   false,
		OpenRe:        nil,
		OpenAfter:     nil,
	}
}

// interpolated returns spec with `${ … }`-style interpolation enabled.
func interpolated(spec StringSpec) StringSpec {
	spec.Interpolation = true
	return spec
}

// openingAfter returns spec that opens only when the preceding residue matches after.
func openingAfter(spec StringSpec, after *regexp.Regexp) StringSpec {
	spec.OpenAfter = after
	return spec
}

// newLangConfig returns a config with no comments or strings; callers set what the language uses.
func newLangConfig(id LanguageID, directive *regexp.Regexp) *LangConfig {
	return &LangConfig{
		ID:               id,
		LineComment:      nil,
		LineCommentAfter: nil,
		BlockComment:     false,
		BlockOpen:        "",
		BlockClose:       "",
		NestBlock:        false,
		RustChar:         false,
		RegexLiteral:     false,
		Strings:          nil,
		Expr:             nil,
		IndentScalar:     nil,
		Heredoc:          nil,
		Directive:        directive,
	}
}

func dq() StringSpec { return newStringSpec(`"`, `"`, EscapeBackslash, false) }

func sq() StringSpec { return newStringSpec(`'`, `'`, EscapeBackslash, false) }

func langGo() *LangConfig {
	lang := newLangConfig(LanguageGo, goDirective)
	lang.LineComment = []string{"//"}
	lang.BlockComment = true
	lang.Strings = []StringSpec{dq(), sq(), newStringSpec("`", "`", EscapeNone, true)}
	return lang
}

func langTS() *LangConfig {
	lang := newLangConfig(LanguageTS, tsDirective)
	lang.LineComment = []string{"//"}
	lang.BlockComment = true
	lang.RegexLiteral = true
	lang.Strings = []StringSpec{dq(), sq(), interpolated(newStringSpec("`", "`", EscapeBackslash, true))}
	return lang
}

// langRust treats `"…"` as multiline: a Rust string legally spans lines.
func langRust() *LangConfig {
	lang := newLangConfig(LanguageRust, rustDirective)
	lang.LineComment = []string{"//"}
	lang.BlockComment = true
	lang.NestBlock = true
	lang.RustChar = true
	lang.Strings = []StringSpec{newStringSpec(`"`, `"`, EscapeBackslash, true)}
	return lang
}

func langNix() *LangConfig {
	lang := newLangConfig(LanguageNix, nixDirective)
	lang.LineComment = []string{"#"}
	lang.BlockComment = true
	lang.Strings = []StringSpec{
		interpolated(newStringSpec("''", "''", EscapeNixIndented, true)),
		interpolated(newStringSpec(`"`, `"`, EscapeBackslash, true)),
	}
	return lang
}

func langPython() *LangConfig {
	lang := newLangConfig(LanguagePython, pythonDirective)
	lang.LineComment = []string{"#"}
	lang.Strings = []StringSpec{
		newStringSpec(`"""`, `"""`, EscapeBackslash, true),
		newStringSpec(`'''`, `'''`, EscapeBackslash, true),
		dq(),
		sq(),
	}
	return lang
}

func langTOML() *LangConfig {
	lang := newLangConfig(LanguageTOML, never)
	lang.LineComment = []string{"#"}
	lang.Strings = []StringSpec{
		newStringSpec(`"""`, `"""`, EscapeBackslash, true),
		newStringSpec(`'''`, `'''`, EscapeNone, true),
		dq(),
		newStringSpec(`'`, `'`, EscapeNone, false),
	}
	return lang
}

func langYAML() *LangConfig {
	lang := newLangConfig(LanguageYAML, yamlDirective)
	lang.LineComment = []string{"#"}
	lang.LineCommentAfter = yamlCommentAfter
	lang.Strings = []StringSpec{
		openingAfter(newStringSpec(`"`, `"`, EscapeBackslash, true), yamlValueStart),
		openingAfter(newStringSpec(`'`, `'`, EscapeDoubled, true), yamlValueStart),
	}
	lang.IndentScalar = indentScalar
	return lang
}

func shAnsiC() StringSpec { return newStringSpec("$'", "'", EscapeBackslash, false) }

func shSingle() StringSpec { return newStringSpec("'", "'", EscapeNone, true) }

func shBacktick() StringSpec { return newStringSpec("`", "`", EscapeBackslash, true) }

// langShellInterp scans the body of a `$( … )` substitution inside a double-quoted string.
func langShellInterp() *LangConfig {
	lang := newLangConfig(LanguageShell, never)
	lang.Strings = []StringSpec{
		shAnsiC(),
		newStringSpec(`"`, `"`, EscapeBackslash, true),
		shSingle(),
		shBacktick(),
	}
	return lang
}

func langShell() *LangConfig {
	doubleQuoted := interpolated(newStringSpec(`"`, `"`, EscapeBackslash, true))
	doubleQuoted.InterpOpen = "$("
	doubleQuoted.InterpClose = ")"
	doubleQuoted.InterpLang = langShellInterp()

	lang := newLangConfig(LanguageShell, shellDirective)
	lang.LineComment = []string{"#"}
	lang.LineCommentAfter = shellCommentAfter
	lang.Strings = []StringSpec{
		newStringSpec("$((", "))", EscapeNone, true),
		shAnsiC(),
		doubleQuoted,
		shSingle(),
		shBacktick(),
	}
	lang.Heredoc = shellHeredoc
	return lang
}

func langSQL() *LangConfig {
	tagged := newStringSpec("$tag$", "", EscapeNone, true)
	tagged.CloseIsOpen = true
	tagged.OpenRe = sqlTagOpen

	lang := newLangConfig(LanguageSQL, sqlDirective)
	lang.LineComment = []string{"--"}
	lang.BlockComment = true
	lang.NestBlock = true
	lang.Strings = []StringSpec{
		newStringSpec("$$", "$$", EscapeNone, true),
		tagged,
		newStringSpec("E'", "'", EscapeBackslash, true),
		newStringSpec("'", "'", EscapeDoubled, true),
		newStringSpec(`"`, `"`, EscapeDoubled, false),
	}
	return lang
}

func langKDL() *LangConfig {
	lang := newLangConfig(LanguageKDL, never)
	lang.LineComment = []string{"//"}
	lang.BlockComment = true
	lang.NestBlock = true
	lang.Strings = []StringSpec{
		newStringSpec(`#"`, `"#`, EscapeNone, true),
		newStringSpec(`"`, `"`, EscapeBackslash, true),
	}
	return lang
}

func langCSS() *LangConfig {
	url := newStringSpec("url(", ")", EscapeBackslash, true)
	url.OpenRe = cssURLOpenRe

	lang := newLangConfig(LanguageCSS, cssDirective)
	lang.BlockComment = true
	lang.Strings = []StringSpec{url, dq(), sq()}
	return lang
}

// langHTML is the `.astro` template: HTML comments, attribute strings, and TS `{ … }` expressions.
func langHTML() *LangConfig {
	lang := newLangConfig(LanguageHTML, htmlDirective)
	lang.BlockComment = true
	lang.BlockOpen = "<!--"
	lang.BlockClose = "-->"
	lang.Strings = []StringSpec{
		openingAfter(newStringSpec(`"`, `"`, EscapeNone, true), attrValueStart),
		openingAfter(newStringSpec(`'`, `'`, EscapeNone, true), attrValueStart),
	}
	lang.Expr = &ExprSpec{Open: "{", Close: "}", Lang: langTS()}
	return lang
}

func langProto() *LangConfig {
	lang := newLangConfig(LanguageProto, never)
	lang.LineComment = []string{"//"}
	lang.BlockComment = true
	lang.Strings = []StringSpec{dq(), sq()}
	return lang
}

// langByExt maps a lowercase file extension to its language config.
func langByExt() map[string]*LangConfig {
	goConfig, ts, python, yaml, shell := langGo(), langTS(), langPython(), langYAML(), langShell()
	return map[string]*LangConfig{
		".go":    goConfig,
		".ts":    ts,
		".js":    ts,
		".mts":   ts,
		".cts":   ts,
		".mjs":   ts,
		".cjs":   ts,
		".rs":    langRust(),
		".nix":   langNix(),
		".py":    python,
		".pyi":   python,
		".toml":  langTOML(),
		".yaml":  yaml,
		".yml":   yaml,
		".sh":    shell,
		".bash":  shell,
		".zsh":   shell,
		".sql":   langSQL(),
		".kdl":   langKDL(),
		".css":   langCSS(),
		".proto": langProto(),
	}
}

// langByID maps a language ID to its config. TSX and Astro use parser-based scanners instead.
func langByID() map[LanguageID]*LangConfig {
	return map[LanguageID]*LangConfig{
		LanguageGo:     langGo(),
		LanguageTS:     langTS(),
		LanguageRust:   langRust(),
		LanguageNix:    langNix(),
		LanguagePython: langPython(),
		LanguageTOML:   langTOML(),
		LanguageYAML:   langYAML(),
		LanguageShell:  langShell(),
		LanguageSQL:    langSQL(),
		LanguageKDL:    langKDL(),
		LanguageCSS:    langCSS(),
		LanguageHTML:   langHTML(),
		LanguageProto:  langProto(),
	}
}

// cssURLOpen implements the CSS URL negative lookahead without regex lookahead.
func cssURLOpen(text string) (string, bool) {
	if len(text) < len("url(") || !strings.EqualFold(text[:len("url(")], "url(") {
		return "", false
	}
	for i := len("url("); i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '"', '\'':
			return "", false
		default:
			return text[:len("url(")], true
		}
	}
	return text[:len("url(")], true
}
