// Language configurations and lookup maps. TS is defined before HTML because
// HTML's expression scanner refers to it.

import type { LangConfig, LanguageId, StringSpec } from "./scan.ts";

// A language with no tool-read comment family still has to answer the
// question, so it answers with a regex that cannot match.
const NEVER = /$^/;

const dq: StringSpec = {
	open: '"',
	close: '"',
	escape: "backslash",
	multiline: false,
	interpolation: false,
};
const sq: StringSpec = {
	open: "'",
	close: "'",
	escape: "backslash",
	multiline: false,
	interpolation: false,
};
const goBacktick: StringSpec = {
	open: "`",
	close: "`",
	escape: "none",
	multiline: true,
	interpolation: false,
};
const tsBacktick: StringSpec = {
	open: "`",
	close: "`",
	escape: "backslash",
	multiline: true,
	interpolation: true,
};

// `//go:…` covers build/generate/embed/linkname; `//nolint`, `//noinspection`
// and the `//lint:` family are read by linters.
const GO_DIRECTIVE = /^\/\/\s*(go:|nolint|lint:|noinspection|export\s|cgo\s)/;

// TypeScript directives include compiler pragmas, linter suppressions, and
// triple-slash references. Match both // and /* forms: JSX needs block-form
// suppressions, and Astro expressions use TypeScript comments. jsx.ts uses
// this regex too, keeping both comment routes consistent.
export const TS_DIRECTIVE =
	/^(?:\/\/[/!]?|\/\*)\s*(@ts-|<reference|biome-ignore|eslint|prettier-ignore|istanbul\s|c8\s|v8\s|@jsx|@license|@preserve|webpack|@vite-)/;

const GO: LangConfig = {
	id: "go",
	lineComment: ["//"],
	blockComment: true,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [dq, sq, goBacktick],
	directive: GO_DIRECTIVE,
};

export const TS: LangConfig = {
	id: "ts",
	lineComment: ["//"],
	blockComment: true,
	nestBlock: false,
	rustChar: false,
	regexLiteral: true,
	strings: [dq, sq, tsBacktick],
	directive: TS_DIRECTIVE,
};

// Unlike Go's or TypeScript's, a Rust `"…"` legally spans lines, so it is not
// a single-line spec: closing it at EOL would report well-formed source as
// malformed.
const rustString: StringSpec = {
	open: '"',
	close: '"',
	escape: "backslash",
	multiline: true,
	interpolation: false,
};

const RUST: LangConfig = {
	id: "rust",
	// `///` and `//!` are `//`-prefixed, so the `//` token covers doc comments.
	lineComment: ["//"],
	blockComment: true,
	nestBlock: true,
	// `'` is a char literal OR a lifetime in Rust; a naive string open would
	// swallow the rest of a `&'a str` line. rustChar disambiguates.
	rustChar: true,
	regexLiteral: false,
	strings: [rustString],
	// Rust's real directives are attributes (`#[...]`), which are code already;
	// these are the comment-shaped ones tools read.
	directive: /^\/\/[/!]?\s*(rustfmt::|clippy::|@generated|cargo:)/,
};

const NIX: LangConfig = {
	id: "nix",
	lineComment: ["#"],
	blockComment: true,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: "''",
			close: "''",
			escape: "nix-indented",
			multiline: true,
			interpolation: true,
		},
		{
			open: '"',
			close: '"',
			escape: "backslash",
			multiline: true,
			interpolation: true,
		},
	],
	// A leading `#!` is an interpreter line, not a comment to rewrite.
	directive: /^#!|^#\s*(nix:|shellcheck\s|nixfmt:|deadnix:)/,
};

const PY: LangConfig = {
	id: "python",
	lineComment: ["#"],
	// Python has no block comment; triple-quoted strings are expressions, not
	// comments, so a docstring change is a code change here.
	blockComment: false,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: '"""',
			close: '"""',
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
		{
			open: "'''",
			close: "'''",
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
		dq,
		sq,
	],
	// `# type:` and `# noqa` are read by mypy/flake8; `#!` is the interpreter;
	// `# -*- coding:` sets the source encoding.
	directive: /^#!|^#\s*(type:|noqa|pylint:|mypy:|ruff:|fmt:|-\*-\s*coding)/,
};

const TOML: LangConfig = {
	id: "toml",
	// TOML forbids `#` in bare keys and unquoted values, so `a=1#c` really is a
	// comment — no preceding-space rule.
	lineComment: ["#"],
	blockComment: false,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: '"""',
			close: '"""',
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
		{
			open: "'''",
			close: "'''",
			escape: "none",
			multiline: true,
			interpolation: false,
		},
		dq,
		{
			open: "'",
			close: "'",
			escape: "none",
			multiline: false,
			interpolation: false,
		},
	],
	directive: NEVER,
};

// A YAML quote opens a scalar only at a value start (line start, or after
// `:`/`-`/`[`/`{`/`,`/`?`). Without this, `key: it's fine` opens a string that
// runs to the next apostrophe and hides a later scalar's `#`.
const YAML_VALUE_START = /(^|[:\-[{,?])$/;

const YAML: LangConfig = {
	id: "yaml",
	lineComment: ["#"],
	// Only after whitespace: `url: http://a#f` is a plain scalar, not a comment.
	lineCommentAfter: /[ \t]/,
	blockComment: false,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: '"',
			close: '"',
			escape: "backslash",
			multiline: true,
			interpolation: false,
			openAfter: YAML_VALUE_START,
		},
		{
			open: "'",
			close: "'",
			// YAML single-quoted scalars have no backslash escape: `''` is a quote.
			escape: "doubled",
			multiline: true,
			interpolation: false,
			openAfter: YAML_VALUE_START,
		},
	],
	// A block-scalar indicator at end of the code residue: `key: |`, `- >-`,
	// `key: !!str |`, `key: &anchor |2`. Group 2 is the explicit indentation
	// indicator, which fixes the body column instead of the first body line.
	indentScalar:
		/(^|:|-)[ \t]*(?:(?:!!?[\w/.-]*|&[\w-]+|\*[\w-]+)[ \t]+)*[|>][-+]?([0-9]?)[-+]?[ \t]*(#.*)?$/,
	// `yaml-language-server:` enables schema validation; paired `yamllint`
	// directives bracket a lint region and must stay in the code residue.
	// Generated banners are read by humans and tools. `{{` marks template actions.
	directive:
		/^#\s*(yaml-language-server:|yamllint\s|renovate:|prettier-ignore|GENERATED|DO NOT EDIT)|^\{\{/,
};

const shAnsiC: StringSpec = {
	open: "$'",
	close: "'",
	escape: "backslash",
	multiline: false,
	interpolation: false,
};
const shSingle: StringSpec = {
	open: "'",
	close: "'",
	escape: "none",
	multiline: true,
	interpolation: false,
};
const shBacktick: StringSpec = {
	open: "`",
	close: "`",
	escape: "backslash",
	multiline: true,
	interpolation: false,
};
// Inside `$( … )` a `#` is conservatively CONTENT: a real comment there would
// be stripped from the residue while the surrounding string stays, so the gate
// errs toward "this line is code" and a sweep that edits one must skip it.
const SH_INTERP: LangConfig = {
	id: "shell",
	lineComment: [],
	blockComment: false,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		shAnsiC,
		{
			open: '"',
			close: '"',
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
		shSingle,
		shBacktick,
	],
	directive: NEVER,
};

const SH: LangConfig = {
	id: "shell",
	lineComment: ["#"],
	// A shell comment opens at a WORD start: line start, whitespace, or after
	// `;&|(`. So `echo hi;# note` is a comment, while `${x#prefix}` and
	// `${#arr[@]}` are parameter expansions.
	lineCommentAfter: /[ \t;&|(]/,
	blockComment: false,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [
		// `$(( … ))` is opaque: `a << b` inside would open a bogus heredoc, and
		// `$((16#ff))` is a base prefix. Before `"` and `$(` for longest-open.
		{
			open: "$((",
			close: "))",
			escape: "none",
			multiline: true,
			interpolation: false,
		},
		shAnsiC,
		{
			open: '"',
			close: '"',
			escape: "backslash",
			multiline: true,
			interpolation: true,
			interpOpen: "$(",
			interpClose: ")",
			interpLang: SH_INTERP,
		},
		shSingle,
		shBacktick,
	],
	// Four alternatives, not a backreference: `<<\EOF` has no closing quote, so
	// a backreferenced form would silently fail to match and every `#` line in
	// the body would strip as a comment. `<<<` (here-string) matches none of
	// them, which is what keeps it from opening a frame.
	heredoc:
		/<<-?[ \t]*(?:\\([A-Za-z_]\w*)|'([A-Za-z_]\w*)'|"([A-Za-z_]\w*)"|([A-Za-z_]\w*))/y,
	directive: /^#!|^#\s*shellcheck\s/,
};

const SQL: LangConfig = {
	id: "sql",
	// `x--c` is a comment in SQL; no preceding-space rule.
	lineComment: ["--"],
	blockComment: true,
	// Postgres nests block comments; SQLite does not, and nesting is the
	// conservative reading (an unmatched inner `*/` fails as underflow).
	nestBlock: true,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: "$$",
			close: "$$",
			escape: "none",
			multiline: true,
			interpolation: false,
		},
		{
			// `open` is documentation here: closeIsOpen takes the open from `openRe`
			// and the close from the text that matched it.
			open: "$tag$",
			close: "",
			closeIsOpen: true,
			openRe: /\$[A-Za-z_][A-Za-z0-9_]*\$/y,
			escape: "none",
			multiline: true,
			interpolation: false,
		},
		{
			open: "E'",
			close: "'",
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
		{
			open: "'",
			close: "'",
			escape: "doubled",
			multiline: true,
			interpolation: false,
		},
		{
			open: '"',
			close: '"',
			escape: "doubled",
			multiline: false,
			interpolation: false,
		},
	],
	// sqlc reads `-- name: X :one` to generate a method; it also copies the
	// comment block above it into the generated Go doc comment.
	directive: /^--\s*name:/,
};

const KDL: LangConfig = {
	id: "kdl",
	lineComment: ["//"],
	blockComment: true,
	// KDL block comments nest.
	nestBlock: true,
	rustChar: false,
	regexLiteral: false,
	strings: [
		{
			open: '#"',
			close: '"#',
			escape: "none",
			multiline: true,
			interpolation: false,
		},
		{
			open: '"',
			close: '"',
			escape: "backslash",
			multiline: true,
			interpolation: false,
		},
	],
	// `/-` (slashdash) disables the next node or argument — a semantic edit to
	// the document, not prose. It is not listed as a comment token, so it falls
	// through to the residue as two ordinary code characters.
	directive: NEVER,
};

// Unquoted CSS url() targets are opaque through `)`: `/*` inside a URL is
// content, not a comment. CSS forbids unescaped parentheses in such targets.
// Quoted targets use normal string handling; their content may include `)`.
// The matcher is case-insensitive and multiline for whitespace in url().
const cssUrl: StringSpec = {
	open: "url(",
	close: ")",
	openRe: /url\((?![ \t\r\n]*["'])/iy,
	escape: "backslash",
	multiline: true,
	interpolation: false,
};

export const CSS: LangConfig = {
	id: "css",
	// `//` is NOT a comment in CSS.
	lineComment: [],
	blockComment: true,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	// `url(` first: it is the longest open at a position where it matches, and
	// a quoted target inside it is ordinary body text either way.
	strings: [cssUrl, dq, sq],
	// CSS linters use these suppression families in source files.
	directive: /^\/\*\s*(stylelint-|biome-ignore|postcss-)/,
};

// An HTML attribute value opens only after `=`, so an apostrophe in template
// prose (`<p>it's fine</p>`) cannot open a string that swallows the rest of the
// document. Attribute values legally span lines, so they are multiline specs:
// one left open at EOF is then reported as unclosed rather than guessed at.
const ATTR_VALUE_START = /=$/;

// HTML comments the toolchain READS. The HTML route STRIPS comments, so
// without this a `<!-- prettier-ignore -->` could be reworded or deleted while
// the gate still reported comments-only. A conditional comment (`<!--[if …]>`)
// selects markup, so it is code as well.
const HTML_DIRECTIVE =
	/^<!--\s*(prettier-ignore|eslint|biome-ignore|markdownlint|rumdl|htmlhint|\[if\b)/;

// The `.astro` template between the frontmatter fence and the islands: HTML
// comments, attribute strings, and `{ … }` expressions holding TypeScript.
export const HTML: LangConfig = {
	id: "html",
	lineComment: [],
	blockComment: true,
	blockOpen: "<!--",
	blockClose: "-->",
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	// HTML attribute values have no backslash escape.
	strings: [
		{
			open: '"',
			close: '"',
			escape: "none",
			multiline: true,
			interpolation: false,
			openAfter: ATTR_VALUE_START,
		},
		{
			open: "'",
			close: "'",
			escape: "none",
			multiline: true,
			interpolation: false,
			openAfter: ATTR_VALUE_START,
		},
	],
	expr: { open: "{", close: "}", lang: TS },
	directive: HTML_DIRECTIVE,
};

const PROTO: LangConfig = {
	id: "proto",
	lineComment: ["//"],
	blockComment: true,
	nestBlock: false,
	rustChar: false,
	regexLiteral: false,
	strings: [dq],
	directive: NEVER,
};

export const LANG_BY_EXT: Readonly<Record<string, LangConfig>> = {
	".go": GO,
	".ts": TS,
	".js": TS,
	".mts": TS,
	".cts": TS,
	".mjs": TS,
	".cjs": TS,
	".rs": RUST,
	".nix": NIX,
	".py": PY,
	".pyi": PY,
	".toml": TOML,
	".yaml": YAML,
	".yml": YAML,
	".sh": SH,
	".bash": SH,
	".zsh": SH,
	".sql": SQL,
	".kdl": KDL,
	".css": CSS,
	".proto": PROTO,
};

// `.tsx`/`.jsx`/`.astro` use parser-based scanners rather than this table.
export const LANG_BY_ID: Readonly<Partial<Record<LanguageId, LangConfig>>> = {
	go: GO,
	ts: TS,
	rust: RUST,
	nix: NIX,
	python: PY,
	toml: TOML,
	yaml: YAML,
	shell: SH,
	sql: SQL,
	kdl: KDL,
	css: CSS,
	html: HTML,
	proto: PROTO,
};
