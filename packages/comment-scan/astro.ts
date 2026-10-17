// `.astro` is three region kinds in one file — a `---` frontmatter fence
// holding TypeScript, an HTML-ish template, and `<script>`/`<style>` islands —
// so it is segmented and each region scanned by the LangConfig it already has.
// A shape outside the modelled ones fails closed rather than being guessed at.

import { scanJsx } from "./jsx.ts";
import { CSS, HTML, TS } from "./languages.ts";
import {
	DEFAULT_STATE,
	type LangConfig,
	type ScanState,
	scanLines,
} from "./scan.ts";

export type Segment = {
	lang: LangConfig;
	startLine: number;
	lines: string[];
};

// An island tag must own its whole line: every island in the corpus has this
// shape, and an open tag sharing a line with body text is a shape this
// segmenter does not model.
const SCRIPT_OPEN = /^\s*<script(\s[^>]*)?>\s*$/;
const STYLE_OPEN = /^\s*<style(\s[^>]*)?>\s*$/;
const SCRIPT_CLOSE = /^\s*<\/script>\s*$/;
const STYLE_CLOSE = /^\s*<\/style>\s*$/;
// `<script is:inline set:html={…}></script>` on one line has an empty body and
// is ordinary template residue.
const ONE_LINE_ISLAND = /<(script|style)(\s[^>]*)?>[^<]*<\/\1>/;
const ISLAND_MENTION = /<(script|style)\b/;
const FENCE = /^---\s*$/;

// A markup-returning `{ … }` goes to the parser, because a scanner would treat
// a `//` in JsxText as a comment and strip the closing `}` with it. Only the
// expression's own lines go, never the whole template: a template holds
// `<!DOCTYPE html>`, void tags and bare `&`, none of which parse as TSX.
const MARKUP_EXPR = /\{[^}]*<[A-Za-z/]/;
// An expression may span lines, putting the `<` below its `{`.
const EXPR_OPEN = /\{[^}]*$/;
const MARKUP_LINE = /<[A-Za-z/]/;

// The lines of the `{ … }` region starting at `from`, or null when it does not
// close (or holds no markup, so the scanner handles it). Brace depth is
// counted on the region text, which is exact for the shapes here: a `{` inside
// a string or comment would need the parser this function is selecting.
function markupRegion(lines: readonly string[], from: number): string[] | null {
	const first = lines[from] ?? "";
	if (!MARKUP_EXPR.test(first) && !EXPR_OPEN.test(first)) return null;
	let depth = 0;
	let markup = MARKUP_EXPR.test(first);
	let quote: '"' | "'" | "`" | null = null;
	let lineComment = false;
	for (let k = from; k < lines.length; k++) {
		const line = lines[k] ?? "";
		lineComment = false;
		if (k > from && MARKUP_LINE.test(line) && quote === null) markup = true;
		for (let j = 0; j < line.length; j++) {
			const ch = line[j] ?? "";
			const next = line[j + 1] ?? "";
			if (lineComment) continue;
			// Before markup begins this is TypeScript, where `//` starts a comment.
			// After markup begins it may be JSX text such as `http://x`; the TSX
			// parser owns comment placement inside that region.
			if (!markup && quote === null && ch === "/" && next === "/") {
				lineComment = true;
				break;
			}
			if (quote !== null) {
				if (ch === "\\") {
					j++;
					continue;
				}
				if (ch === quote) quote = null;
				continue;
			}
			if (ch === '"' || ch === "'" || ch === "`") {
				quote = ch;
				continue;
			}
			if (ch === "{") depth++;
			else if (ch === "}") depth--;
		}
		if (depth <= 0 && quote === null)
			return markup ? lines.slice(from, k + 1) : null;
	}
	return markup ? lines.slice(from) : null;
}

type Walk = {
	segments: Segment[];
	residues: string[];
	ambiguous: boolean;
};

const UNMODELLED: Walk = { segments: [], residues: [], ambiguous: true };

// One stateful pass: boundaries and residues come out together, because
// deciding whether a `<script` opens an island REQUIRES the scanner state. A
// `<script>` named inside an `<!-- … -->` is prose, and a textual check would
// reject two real corpus layouts as unmodelled.
function walkAstro(text: string): Walk {
	const lines = text.split("\n");
	const segments: Segment[] = [];
	const residues: string[] = [];
	let ambiguous = false;
	let i = 0;

	// Frontmatter, only when the file opens with the fence. The fence lines
	// themselves are template residue: they are load-bearing structure.
	if (FENCE.test(lines[0] ?? "")) {
		let close = -1;
		for (let k = 1; k < lines.length; k++) {
			if (FENCE.test(lines[k] ?? "")) {
				close = k;
				break;
			}
		}
		// An unclosed fence means the whole file's regions are unknown.
		if (close === -1) return UNMODELLED;
		const open = lines[0] ?? "";
		segments.push({ lang: HTML, startLine: 0, lines: [open] });
		residues.push(open);
		const body = lines.slice(1, close);
		segments.push({ lang: TS, startLine: 1, lines: body });
		const scan = scanLines(TS, body);
		// The frontmatter must close every construct it opens; an unterminated
		// string or block there would bleed into the template.
		if (scan.underflow || scan.state.stack.length > 0) ambiguous = true;
		residues.push(...scan.residues);
		const fence = lines[close] ?? "";
		segments.push({ lang: HTML, startLine: close, lines: [fence] });
		residues.push(fence);
		i = close + 1;
	}

	let template: string[] = [];
	let templateStart = i;
	let state: ScanState = DEFAULT_STATE;
	const openTemplate = (at: number): void => {
		template = [];
		templateStart = at;
	};
	const closeTemplate = (): void => {
		if (template.length === 0) return;
		segments.push({ lang: HTML, startLine: templateStart, lines: template });
		template = [];
	};

	while (i < lines.length) {
		const line = lines[i] ?? "";

		// A markup expression takes the parser path, but only from a clean state:
		// mid string or mid comment the line is already payload, and re-parsing it
		// standalone would read it out of context.
		const region = state.stack.length === 0 ? markupRegion(lines, i) : null;
		if (region) {
			// The WHOLE region is parsed as one unit: parsing line-by-line would
			// hand the parser a fragment, and a fragment holding only the JSX half
			// of the expression falls back to the token scanner, where a `//` in
			// element text strips the rest of the line.
			const parsed = scanJsx(region.join("\n"));
			// A region that does not parse cannot have its comments placed.
			if (parsed.ambiguous || parsed.residues.length !== region.length) {
				return UNMODELLED;
			}
			template.push(...region);
			residues.push(...parsed.residues);
			i += region.length;
			continue;
		}

		const scan = scanLines(HTML, [line], state);
		if (scan.underflow) ambiguous = true;
		const residue = scan.residues[0] ?? "";

		// Island detection reads the RESIDUE, so a tag named inside a comment or
		// an attribute string is prose, not a region boundary.
		const isOpen =
			state.stack.length === 0 &&
			(SCRIPT_OPEN.test(residue) || STYLE_OPEN.test(residue));
		if (!isOpen) {
			if (
				state.stack.length === 0 &&
				ISLAND_MENTION.test(residue) &&
				!ONE_LINE_ISLAND.test(residue)
			) {
				// An open tag sharing a line with body text: the body would be
				// mis-attributed to the template, so refuse the file.
				return UNMODELLED;
			}
			state = scan.state;
			template.push(line);
			residues.push(residue);
			i++;
			continue;
		}

		const isScript = SCRIPT_OPEN.test(residue);
		const closeRe = isScript ? SCRIPT_CLOSE : STYLE_CLOSE;
		let close = -1;
		for (let k = i + 1; k < lines.length; k++) {
			if (closeRe.test(lines[k] ?? "")) {
				close = k;
				break;
			}
		}
		if (close === -1) return UNMODELLED;
		// The open and close tags stay template residue: `is:inline`, `set:html`
		// and `define:vars` change behaviour, so they are not part of the body.
		template.push(line);
		residues.push(residue);
		closeTemplate();

		const lang = isScript ? TS : CSS;
		const body = lines.slice(i + 1, close);
		segments.push({ lang, startLine: i + 1, lines: body });
		// Each island starts clean and must end clean, for the same reason the
		// frontmatter must.
		const bodyScan = scanLines(lang, body);
		if (bodyScan.underflow || bodyScan.state.stack.length > 0) ambiguous = true;
		residues.push(...bodyScan.residues);

		const closeTag = lines[close] ?? "";
		openTemplate(close);
		template.push(closeTag);
		residues.push(closeTag);
		state = DEFAULT_STATE;
		i = close + 1;
	}
	closeTemplate();
	// A template that ends inside a comment, a string or a `{ … }` expression is
	// malformed: leaving it open would scan later text in the wrong language.
	if (state.stack.length > 0) ambiguous = true;
	return { segments, residues, ambiguous };
}

// The regions in file order. `null` = a shape this segmenter does not model —
// an unclosed frontmatter fence, an unclosed island, or an island tag that does
// not own its line.
export function segmentAstro(text: string): Segment[] | null {
	const walk = walkAstro(text);
	return walk.segments.length === 0 && walk.ambiguous ? null : walk.segments;
}

// One residue per source line, so callers can compare and report Astro lines like any other file.
export function scanAstro(text: string): {
	residues: string[];
	ambiguous: boolean;
} {
	text = text.replace(/\r\n/g, "\n");
	const walk = walkAstro(text);
	if (walk.ambiguous) return { residues: text.split("\n"), ambiguous: true };
	return { residues: walk.residues, ambiguous: false };
}
