// Scanner core: the generic per-language comment/string state machine, and the
// config shape it consumes. The language TABLE lives in languages.ts, which
// imports only TYPES from here — so this module has no runtime import at all
// and the two cannot form a cycle.

// `nix-indented` escapes with `''` itself; `doubled` is the SQL/YAML rule
// where the close token repeated is a literal (`'it''s'`).
export type EscapeMode = "backslash" | "nix-indented" | "doubled" | "none";

export type StringSpec = {
	// The literal open token. With `openRe` the real open is that regex and
	// this literal is documentation only.
	open: string;
	close: string;
	escape: EscapeMode;
	// Only backtick / triple-quote / nix / YAML-quoted strings legally span
	// lines. A spec that is NOT multiline and is still open at EOL is malformed
	// source, not a string to quietly close: the scanner reports it so the
	// caller fails the file closed rather than trusting the residue.
	multiline: boolean;
	// An interpolation inside opens a nested CODE context (TS template `${…}`,
	// Nix antiquote, shell `$(…)`).
	interpolation: boolean;
	// Default `${` … `}`. Shell command substitution is `$(` … `)`.
	interpOpen?: string;
	interpClose?: string;
	// The language whose comment tokens apply inside the interpolation. Unset =
	// keep the enclosing language.
	interpLang?: LangConfig;
	// The matched open text is also the close token (SQL `$tag$ … $tag$`).
	// Requires `openRe`.
	closeIsOpen?: boolean;
	// A STICKY regex matched at the scan position, for an open token whose text
	// is not fixed (SQL `$tag$`, CSS's case-insensitive `url(`).
	openRe?: RegExp;
	// The string only OPENS where this matches the code residue so far. YAML
	// quotes open a scalar only at a value start, so `key: it's fine` does not
	// open a string that would swallow the next real quoted scalar; an HTML
	// attribute value opens only after `=`.
	openAfter?: RegExp;
};

export type LangConfig = {
	id: LanguageId;
	lineComment: readonly string[];
	// Where a line-comment token may START, as a regex over the PRECEDING
	// character. Absent = anywhere. YAML needs whitespace (`url: http://a#f` is
	// a plain scalar); shell needs a word start, which is whitespace or one of
	// `;&|(` (`echo hi;# note` is a comment, `${x#prefix}` is not).
	lineCommentAfter?: RegExp;
	blockComment: boolean;
	// Default `/*` … `*/`; HTML uses `<!--` … `-->`.
	blockOpen?: string;
	blockClose?: string;
	nestBlock: boolean;
	rustChar: boolean;
	// JS/TS `/…/` regex literals: a `/` in expression position opens one, and a
	// `*/` inside it (a `*` quantifier then the closing `/`) is NOT a block close.
	regexLiteral: boolean;
	// Longest-open-first so `"""` wins over `"` and Nix `''` wins over `"`.
	strings: readonly StringSpec[];
	// A code-context expression region whose body is another language: Astro's
	// `{ … }` in template text holds TypeScript. Modelled as an interpolation so
	// the inner context picks up that language's comment tokens.
	expr?: { open: string; close: string; lang: LangConfig };
	// YAML block scalars (`key: |`, `- >`): the body ends by DEDENT, not by a
	// token, so a match here opens an indent-closed frame.
	indentScalar?: RegExp;
	// Shell heredoc opener (`<<EOF`, `<<-'EOF'`, `<<\EOF`): the body starts on
	// the NEXT line and ends at a line equal to the delimiter.
	heredoc?: RegExp;
	// Comments the toolchain READS: `//go:build`, `@ts-expect-error`, `# noqa`.
	// Syntactically comments, semantically code, so editing one changes the
	// build. Tested against the comment text AND the whole trimmed line, so a
	// config language can keep a templated line (YAML `{{ … }}`) out of scanning.
	directive: RegExp;
};

export type LanguageId =
	| "go"
	| "ts"
	| "rust"
	| "nix"
	| "python"
	| "toml"
	| "yaml"
	| "shell"
	| "sql"
	| "kdl"
	| "css"
	| "html"
	| "tsx"
	| "astro"
	| "proto";
// Residues are right-trimmed so a whitespace-only change is never code.
const TRAILING_WS = /[ \t]+$/;

// The scanner state is a stack of frames; an empty stack is code context.
type Frame =
	// A comment. `depth` counts nesting; `keep` holds a tool-read block in the
	// residue.
	| { t: "block"; depth: number; keep: boolean }
	| { t: "str"; spec: StringSpec; close: string }
	// Code scanning resumes inside; `brace` tracks the inner open token so a
	// nested `{}`/`()` does not pop it early.
	| {
			t: "interp";
			brace: number;
			openChar: string;
			close: string;
			lang: LangConfig | null;
	  }
	// A YAML block scalar, closed by dedent.
	| { t: "indent"; col: number | null; parent: number }
	// A shell heredoc body, closed by its delimiter line.
	| { t: "heredoc"; delim: string; stripTabs: boolean };

export type ScanState = { stack: Frame[] };

export const DEFAULT_STATE: ScanState = { stack: [] };

type LineScan = {
	state: ScanState;
	residue: string;
	underflow: boolean;
	malformed: boolean;
};

// Rust char literal at text[i] (a `'`). Returns the consumed length, or 0 when
// this `'` is a lifetime (`'a`) and must be treated as a code character.
function rustCharLen(text: string, i: number): number {
	if (text[i + 1] === "\\") {
		let j = i + 2;
		while (j < text.length && text[j] !== "'") j++;
		return j < text.length ? j - i + 1 : 0;
	}
	return text[i + 2] === "'" ? 3 : 0;
}

// Chars that, as the last non-space code char, put a following `/` in
// expression position (so it opens a regex, not division). The common cases in
// this corpus are `(`, `,`, `=`, `:`, `return `. `)`/`]`/identifier => division.
const REGEX_PRECEDER: Record<string, true> = {
	"(": true,
	",": true,
	"=": true,
	":": true,
	"[": true,
	"!": true,
	"&": true,
	"|": true,
	"?": true,
	"{": true,
	";": true,
	"+": true,
	"-": true,
	"*": true,
	"%": true,
	"<": true,
	">": true,
	"^": true,
	"~": true,
};

// A JS/TS regex literal at text[i] (a `/` in expression position). Returns its
// consumed length through the closing `/` and flags, or 0 when it is not a
// single-line regex. Consuming it is what stops a `*/` inside (a `*`
// quantifier then the close) from reading as a block-comment close.
function regexLiteralLen(text: string, i: number): number {
	let j = i + 1;
	let inClass = false;
	const n = text.length;
	if (j >= n || text[j] === "/" || text[j] === "*") return 0;
	while (j < n) {
		const c = text[j];
		if (c === "\\") {
			j += 2;
			continue;
		}
		if (c === "[") inClass = true;
		else if (c === "]") inClass = false;
		else if (c === "/" && !inClass) {
			j++;
			while (j < n && /[a-z]/.test(text[j] ?? "")) j++;
			return j - i;
		}
		j++;
	}
	return 0;
}

// A sticky (`/y`) regex matched AT the scan position rather than anywhere
// after it — how a `closeIsOpen` string spec and the heredoc opener recognise
// a token whose text is not fixed. The config owns the compiled regex, so this
// only has to set the cursor.
function matchAt(re: RegExp, text: string, i: number): RegExpExecArray | null {
	re.lastIndex = i;
	return re.exec(text);
}

function indentOf(text: string): number {
	let k = 0;
	while (k < text.length && (text[k] === " " || text[k] === "\t")) k++;
	return k;
}

// The nearest enclosing interpolation decides which language's comment tokens
// apply: an Astro `{ … }` holds TypeScript inside an HTML template.
function effectiveLang(lang: LangConfig, stack: readonly Frame[]): LangConfig {
	for (let k = stack.length - 1; k >= 0; k--) {
		const frame = stack[k];
		if (frame?.t === "interp" && frame.lang) return frame.lang;
	}
	return lang;
}

// Whether a line-comment token at text[i] really opens a comment. Line start
// always qualifies; otherwise the language's `lineCommentAfter` decides from
// the preceding character.
function startsLineComment(lang: LangConfig, text: string, i: number): boolean {
	if (!lang.lineCommentAfter || i === 0) return true;
	return lang.lineCommentAfter.test(text[i - 1] ?? "");
}

// Scan one line and return its code residue and exit state. An unmatched block
// close or an unclosed single-line string makes the source malformed, so the
// scanner fails closed.
function scanLine(lang: LangConfig, entry: ScanState, text: string): LineScan {
	const stack: Frame[] = entry.stack.map((f) => ({ ...f }));
	const head = stack[stack.length - 1];

	// A heredoc body is payload verbatim until a line that IS the delimiter.
	if (head?.t === "heredoc") {
		const body = head.stripTabs ? text.replace(/^\t+/, "") : text;
		if (body === head.delim) stack.pop();
		return {
			state: { stack },
			residue: text.replace(TRAILING_WS, ""),
			underflow: false,
			malformed: false,
		};
	}

	// A YAML block scalar body is payload verbatim until the first line that
	// dedents past the body column.
	if (head?.t === "indent") {
		const keep = {
			state: { stack },
			residue: text.replace(TRAILING_WS, ""),
			underflow: false,
			malformed: false,
		};
		if (text.trim() === "") return keep;
		const ind = indentOf(text);
		if (head.col === null) {
			// The body column is the first non-blank line's indentation. A line at
			// or left of the opener's indentation means the scalar was empty.
			if (ind > head.parent) {
				head.col = ind;
				return keep;
			}
		} else if (ind >= head.col) return keep;
		stack.pop();
		return scanLine(lang, { stack }, text);
	}

	let active = effectiveLang(lang, stack);
	const pending: Frame[] = [];
	let residue = "";
	let underflow = false;
	const n = text.length;
	let i = 0;

	while (i < n) {
		const top = stack[stack.length - 1];

		if (top?.t === "block") {
			const open = active.blockOpen ?? "/*";
			const close = active.blockClose ?? "*/";
			if (active.nestBlock && text.startsWith(open, i)) {
				top.depth++;
				if (top.keep) residue += open;
				i += open.length;
				continue;
			}
			if (text.startsWith(close, i)) {
				top.depth--;
				if (top.keep) residue += close;
				if (top.depth === 0) stack.pop();
				i += close.length;
				continue;
			}
			if (top.keep) residue += text[i];
			i++;
			continue;
		}

		if (top?.t === "str") {
			const spec = top.spec;
			const close = top.close;
			if (spec.escape === "backslash" && text[i] === "\\") {
				residue += text.slice(i, i + 2);
				i += 2;
				continue;
			}
			// `''` inside a single-quoted SQL/YAML scalar is a literal quote.
			if (
				spec.escape === "doubled" &&
				text.startsWith(close, i) &&
				text.startsWith(close, i + close.length)
			) {
				residue += text.slice(i, i + close.length * 2);
				i += close.length * 2;
				continue;
			}
			if (spec.escape === "nix-indented" && text.startsWith("''", i)) {
				// `''${` (literal `${`), `'''` (literal `''`), `''\X` (escape) — none
				// close the string; anything else is the closing `''`.
				if (text.startsWith("''${", i)) {
					residue += "''${";
					i += 4;
					continue;
				}
				if (text.startsWith("'''", i)) {
					residue += "'''";
					i += 3;
					continue;
				}
				if (text.startsWith("''\\", i)) {
					const len = Math.min(4, n - i);
					residue += text.slice(i, i + len);
					i += len;
					continue;
				}
				residue += close;
				i += close.length;
				stack.pop();
				continue;
			}
			const interpOpen = spec.interpOpen ?? "${";
			if (spec.interpolation && text.startsWith(interpOpen, i)) {
				residue += interpOpen;
				i += interpOpen.length;
				stack.push({
					t: "interp",
					brace: 0,
					openChar: interpOpen.slice(-1),
					close: spec.interpClose ?? "}",
					lang: spec.interpLang ?? null,
				});
				if (spec.interpLang) active = spec.interpLang;
				continue;
			}
			if (text.startsWith(close, i)) {
				residue += close;
				i += close.length;
				stack.pop();
				continue;
			}
			residue += text[i];
			i++;
			continue;
		}

		// interp frame: track its own nesting, then fall through to code scanning.
		if (top?.t === "interp") {
			if (text.startsWith(top.close, i)) {
				residue += top.close;
				i += top.close.length;
				if (top.brace === 0) {
					stack.pop();
					active = effectiveLang(lang, stack);
				} else top.brace--;
				continue;
			}
			if (text.startsWith(top.openChar, i)) {
				top.brace++;
				residue += top.openChar;
				i += top.openChar.length;
				continue;
			}
		}

		// code context (no frame, or an interp frame continuing).
		const lc = active.lineComment.find((t) => text.startsWith(t, i));
		if (lc !== undefined && startsLineComment(active, text, i)) {
			// A directive is a comment the toolchain reads, so it must survive into
			// the residue: rewriting one changes the build, not just the prose. The
			// whole-line test lets a config language keep a templated line.
			if (
				active.directive.test(text.slice(i)) ||
				active.directive.test(text.trimStart())
			) {
				residue += text.slice(i);
			}
			break;
		}
		const blockOpen = active.blockOpen ?? "/*";
		const blockClose = active.blockClose ?? "*/";
		if (active.blockComment && text.startsWith(blockOpen, i)) {
			// A tool-read block (`/* stylelint-disable */`, `<!-- prettier-ignore -->`)
			// stays in the residue, including its body, until it closes.
			const keep = active.directive.test(text.slice(i));
			stack.push({ t: "block", depth: 1, keep });
			if (keep) residue += blockOpen;
			i += blockOpen.length;
			continue;
		}
		if (active.blockComment && text.startsWith(blockClose, i)) {
			underflow = true;
			i += blockClose.length;
			continue;
		}
		if (active.regexLiteral && text[i] === "/") {
			const trimmed = residue.trimEnd();
			const prev = trimmed.slice(-1);
			const keywordRegex =
				/(?:return|throw|case|delete|void|typeof|instanceof|in|of)\s*$/.test(
					trimmed,
				);
			if (prev === "" || REGEX_PRECEDER[prev] || keywordRegex) {
				const len = regexLiteralLen(text, i);
				if (len > 0) {
					residue += text.slice(i, i + len);
					i += len;
					continue;
				}
			}
		}
		if (active.rustChar && text[i] === "'") {
			const len = rustCharLen(text, i);
			if (len > 0) {
				residue += text.slice(i, i + len);
				i += len;
				continue;
			}
		}
		// A heredoc opens only in TOP-LEVEL code context: a frame queued inside a
		// string would never meet its delimiter and would swallow the file.
		if (active.heredoc && text[i] === "<" && stack.length === 0) {
			const m = matchAt(active.heredoc, text, i);
			const delim = m ? (m[1] ?? m[2] ?? m[3] ?? m[4]) : undefined;
			if (m && delim !== undefined) {
				pending.push({
					t: "heredoc",
					delim,
					stripTabs: text.startsWith("<<-", i),
				});
				residue += m[0];
				i += m[0].length;
				continue;
			}
		}
		const expr = active.expr;
		if (expr && text.startsWith(expr.open, i)) {
			residue += expr.open;
			i += expr.open.length;
			stack.push({
				t: "interp",
				brace: 0,
				openChar: expr.open.slice(-1),
				close: expr.close,
				lang: expr.lang,
			});
			active = expr.lang;
			continue;
		}
		const opened = openString(active, text, i, residue);
		if (opened) {
			residue += opened.open;
			i += opened.open.length;
			stack.push({
				t: "str",
				spec: opened.spec,
				close: opened.spec.closeIsOpen ? opened.open : opened.spec.close,
			});
			continue;
		}
		residue += text[i];
		i++;
	}

	// A single-line string still open at EOL is MALFORMED, not closed. Popping it
	// silently would hand the next line a clean state and let a comment edit below
	// an untrusted lexical state compare equal; the caller fails the file closed.
	let malformed = false;
	const top = stack[stack.length - 1];
	if (top?.t === "str" && !top.spec.multiline) {
		stack.pop();
		malformed = true;
	}
	// Heredoc bodies start on the NEXT line, in opener order: the first `<<A`
	// on `cat <<A <<B` is the first body, so it goes on top of the stack.
	if (pending.length > 0) {
		for (const frame of pending.reverse()) stack.push(frame);
	}
	// A block-scalar indicator at the end of the code residue opens a body whose
	// column comes from the explicit indicator, or from the first body line.
	if (lang.indentScalar && stack.length === 0) {
		const m = lang.indentScalar.exec(residue);
		if (m) {
			const parent = indentOf(text);
			const digit = m[2] ?? "";
			stack.push({
				t: "indent",
				col: digit === "" ? null : parent + Number(digit),
				parent,
			});
		}
	}
	return {
		state: { stack },
		residue: residue.replace(TRAILING_WS, ""),
		underflow,
		malformed,
	};
}

// The first string spec whose open token is at text[i], honouring
// longest-open-first ordering, the regex form, and the value-start gate.
function openString(
	lang: LangConfig,
	text: string,
	i: number,
	residue: string,
): { spec: StringSpec; open: string } | null {
	for (const spec of lang.strings) {
		// An `openRe` spec takes its open from a sticky regex (SQL `$tag$`, CSS
		// `url(`); everything else matches a literal token.
		const open = spec.openRe
			? matchAt(spec.openRe, text, i)?.[0]
			: text.startsWith(spec.open, i)
				? spec.open
				: undefined;
		if (open === undefined) continue;
		// A position-gated spec (YAML quotes, HTML attribute values) only opens
		// where the residue so far allows, so a bare apostrophe in a plain scalar
		// or in template prose cannot open a runaway string.
		if (spec.openAfter && !spec.openAfter.test(residue.trimEnd())) continue;
		return { spec, open };
	}
	return null;
}

export type LinesScan = {
	residues: string[];
	underflow: boolean;
	malformed: boolean;
	state: ScanState;
};

// Scan lines top-to-bottom from an entry state, one residue per line. Exposed
// so a composite file (`.astro`) can scan each region with its own config and
// inspect the state it ended in.
export function scanLines(
	lang: LangConfig,
	lines: readonly string[],
	entry: ScanState = DEFAULT_STATE,
): LinesScan {
	let state = entry;
	let underflow = false;
	let malformed = false;
	const residues: string[] = [];
	for (const line of lines) {
		const scan = scanLine(lang, state, line);
		if (scan.underflow) underflow = true;
		if (scan.malformed) malformed = true;
		state = scan.state;
		residues.push(scan.residue);
	}
	return { residues, underflow, malformed, state };
}

// Scan a complete text. `unclosed` = the file ended inside a frame that should
// have closed (heredoc, dollar quote, multiline string, interpolation, block
// comment), so the rest of it scanned in the wrong context. A YAML block
// scalar (`indent`) is exempt: its body ends by dedent OR by end of file.
export function scanComplete(
	lang: LangConfig,
	text: string,
): {
	residues: string[];
	underflow: boolean;
	unclosed: boolean;
	malformed: boolean;
} {
	const scan = scanLines(lang, text.split("\n"));
	return {
		residues: scan.residues,
		underflow: scan.underflow,
		malformed: scan.malformed,
		unclosed: scan.state.stack.some((frame) => frame.t !== "indent"),
	};
}
