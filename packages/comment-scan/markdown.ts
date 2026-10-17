// Markdown is all prose, so comments-only edits preserve structure rather than
// code residue. This module fingerprints fences, links, headings, directives,
// tables, front matter, and inline code; the checker compares fingerprints.

export type MarkdownItemKind =
	| "front-matter"
	| "fenced-code"
	| "indented-code"
	| "heading"
	| "table-header"
	| "table-delimiter"
	| "directive"
	| "link-target"
	| "link-ref"
	| "autolink"
	| "ref-def"
	| "code-span"
	| "raw-html"
	| "block-marker";

export type MarkdownItem = { kind: MarkdownItemKind; value: string };

// Why a document's structure could not be fingerprinted.
export type MarkdownDefect =
	| "an HTML comment opened and never closed"
	| "front matter opened with `---` and never closed"
	| "a fenced code block opened and never closed";

// A fingerprint plus the defect that makes it untrustworthy. `defect === null`
// is a document whose structure was placed exactly.
export type MarkdownScan = {
	items: MarkdownItem[];
	defect: MarkdownDefect | null;
};

// The comparison's three outcomes. `malformed` is NOT a mismatch: the two
// sides may fingerprint identically and still both be untrustworthy, so it
// must be its own answer rather than a silent pass.
export type MarkdownVerdict =
	| { kind: "same" }
	| {
			kind: "mismatch";
			index: number;
			before: MarkdownItem | null;
			after: MarkdownItem | null;
	  }
	| { kind: "malformed"; side: "before" | "after"; reason: MarkdownDefect };

// A tool-directive HTML comment. These are read by rumdl/markdownlint/prettier,
// so their text is load-bearing; an ordinary `<!-- note -->` is prose.
const DIRECTIVE = /^(rumdl|markdownlint|prettier-ignore)/;

function indentWidth(line: string): number {
	let w = 0;
	for (const ch of line) {
		if (ch === " ") w += 1;
		else if (ch === "\t") w += 4;
		else break;
	}
	return w;
}

function normalizeLabel(label: string): string {
	return label.trim().toLowerCase().replace(/\s+/g, " ");
}

// A GFM table delimiter row: only pipes, colons, dashes, spaces, and every
// cell is `:?-+:?`. This — not the header text — is what marks a table.
function isAlignmentRow(line: string): boolean {
	const t = line.trim();
	if (t === "" || !/^[|:\-\s]+$/.test(t)) return false;
	const cells = t.replace(/^\|/, "").replace(/\|$/, "").split("|");
	return cells.length > 0 && cells.every((c) => /^\s*:?-+:?\s*$/.test(c));
}

// A line that can carry a Setext underline beneath it: ordinary paragraph text,
// not a block opener (fence/heading/list/quote/ref-def) or indented code.
function isParagraph(line: string): boolean {
	if (/^\s*$/.test(line)) return false;
	if (/^ {0,3}(`{3,}|~{3,})/.test(line)) return false;
	if (/^ {0,3}#{1,6}(\s|$)/.test(line)) return false;
	if (/^ {0,3}([-*+]|\d{1,9}[.)])(\s|$)/.test(line)) return false;
	if (/^ {0,3}>/.test(line)) return false;
	if (/^ {0,3}\[[^\]]+\]:/.test(line)) return false;
	return indentWidth(line) < 4;
}

function backtickRun(s: string, i: number): number {
	let j = i;
	while (j < s.length && s.charAt(j) === "`") j += 1;
	return j - i;
}

// A code span closes on a backtick run of the SAME length (CommonMark), so a
// longer run inside a shorter span is content, not a close.
function findCodeClose(s: string, from: number, len: number): number {
	let i = from;
	while (i < s.length) {
		if (s.charAt(i) === "`") {
			const r = backtickRun(s, i);
			if (r === len) return i;
			i += r;
		} else i += 1;
	}
	return -1;
}

// Index of the `]` that closes the `[` at `open`, honoring backslash escapes
// and nested brackets in the link text.
function matchBracket(s: string, open: number): number {
	let depth = 0;
	for (let i = open; i < s.length; i += 1) {
		const ch = s.charAt(i);
		if (ch === "\\") {
			i += 1;
			continue;
		}
		if (ch === "[") depth += 1;
		else if (ch === "]") {
			depth -= 1;
			if (depth === 0) return i;
		}
	}
	return -1;
}

// Parse an inline destination starting at the `(` of `](target "title")`. The
// target may be `<…>`-wrapped or hold balanced parens; the title is prose.
function parseInlineDest(
	s: string,
	paren: number,
): { target: string; end: number } | null {
	let i = paren + 1;
	while (i < s.length && /\s/.test(s.charAt(i))) i += 1;
	let target = "";
	if (s.charAt(i) === "<") {
		i += 1;
		while (i < s.length && s.charAt(i) !== ">") {
			if (s.charAt(i) === "\\") {
				target += s.charAt(i + 1);
				i += 2;
				continue;
			}
			target += s.charAt(i);
			i += 1;
		}
		if (s.charAt(i) !== ">") return null;
		i += 1;
	} else {
		let depth = 0;
		while (i < s.length) {
			const ch = s.charAt(i);
			if (ch === "\\") {
				target += ch + s.charAt(i + 1);
				i += 2;
				continue;
			}
			if (/\s/.test(ch)) break;
			if (ch === "(") depth += 1;
			if (ch === ")") {
				if (depth === 0) break;
				depth -= 1;
			}
			target += ch;
			i += 1;
		}
	}
	// Skip an optional title to the closing paren; a title containing `)` is
	// rare enough to leave to fail-closed.
	while (i < s.length && s.charAt(i) !== ")") i += 1;
	if (s.charAt(i) !== ")") return null;
	return { target, end: i + 1 };
}

function matchAutolink(
	line: string,
	i: number,
): { uri: string; end: number } | null {
	const rest = line.slice(i);
	const uri = /^<([a-zA-Z][a-zA-Z0-9+.-]{1,31}:[^<>\s]*)>/.exec(rest);
	const mail = /^<([^<>\s@]+@[^<>\s]+\.[^<>\s]+)>/.exec(rest);
	const m = uri ?? mail;
	const g = m?.[1];
	if (!m || g === undefined) return null;
	return { uri: g, end: i + m[0].length };
}

// A bare URL in running prose. Parse balanced parentheses so destinations such
// as `/path_(old)` are not silently truncated.
function matchBareUrl(
	line: string,
	i: number,
): { uri: string; end: number } | null {
	const rest = line.slice(i);
	const prefix = /^(https?:\/\/[^\s<>[\]]+)/.exec(rest)?.[1];
	if (!prefix) return null;
	let depth = 0;
	let end = 0;
	for (; end < prefix.length; end += 1) {
		const ch = prefix.charAt(end);
		if (ch === "(") depth += 1;
		else if (ch === ")") {
			if (depth === 0) break;
			depth -= 1;
		}
	}
	if (depth !== 0) return null;
	const uri = prefix.slice(0, end).replace(/[.,;:!?]+$/, "");
	return uri.length === 0 ? null : { uri, end: i + uri.length };
}

type LinkHit = { kind: "link-target" | "link-ref"; value: string; end: number };

// A `[` opener: inline `(target)`, full/collapsed reference `[id]`, or a
// shortcut `[text]` that is only a link when `text` names a known definition.
function parseLink(
	line: string,
	open: number,
	refLabels: ReadonlySet<string>,
): LinkHit | null {
	if (line.charAt(open) !== "[") return null;
	const close = matchBracket(line, open);
	if (close === -1) return null;
	const textRaw = line.slice(open + 1, close);
	const after = line.charAt(close + 1);
	if (after === "(") {
		const dest = parseInlineDest(line, close + 1);
		return dest
			? { kind: "link-target", value: dest.target, end: dest.end }
			: null;
	}
	if (after === "[") {
		const idClose = line.indexOf("]", close + 2);
		if (idClose !== -1) {
			const id = line.slice(close + 2, idClose).trim();
			const label = id === "" ? textRaw : id;
			return {
				kind: "link-ref",
				value: normalizeLabel(label),
				end: idClose + 1,
			};
		}
	}
	const norm = normalizeLabel(textRaw);
	if (refLabels.has(norm))
		return { kind: "link-ref", value: norm, end: close + 1 };
	return null;
}

function refDefTarget(rest: string): string {
	const s = rest.trim();
	if (s.startsWith("<")) {
		const e = s.indexOf(">");
		return e === -1 ? s : s.slice(1, e);
	}
	const sp = s.search(/\s/);
	return sp === -1 ? s : s.slice(0, sp);
}

const FRONT_OPEN = /^---\s*$/;
const FRONT_CLOSE = /^(---|\.\.\.)\s*$/;
const FENCE_OPEN = /^(?: {0,3}(?:> ?|(?:[-*+] |\d+[.)] ))*)(`{3,}|~{3,})(.*)$/;
const FENCE_CLOSE =
	/^(?: {0,3}(?:> ?|(?:[-*+] |\d+[.)] ))*)(`{3,}|~{3,})[ \t]*$/;

// Reference labels defined anywhere outside fences/front matter. Collected up
// front because a shortcut reference `[x]` may appear before its `[x]:` line.
function collectRefLabels(lines: readonly string[]): Set<string> {
	const set = new Set<string>();
	let inFence = false;
	let fenceChar = "";
	let fenceLen = 0;
	let start = 0;
	const first = lines[0];
	if (first !== undefined && FRONT_OPEN.test(first)) {
		let j = 1;
		while (j < lines.length && !FRONT_CLOSE.test(lines[j] ?? "")) j += 1;
		if (j < lines.length) start = j + 1;
	}
	for (let i = start; i < lines.length; i += 1) {
		const line = lines[i];
		if (line === undefined) continue;
		if (inFence) {
			const m = FENCE_CLOSE.exec(line);
			const bar = m?.[1];
			if (bar && bar.charAt(0) === fenceChar && bar.length >= fenceLen)
				inFence = false;
			continue;
		}
		const fo = FENCE_OPEN.exec(line);
		const bar = fo?.[1];
		if (bar) {
			const ch = bar.charAt(0);
			const info = (fo?.[2] ?? "").trim();
			if (!(ch === "`" && info.includes("`"))) {
				inFence = true;
				fenceChar = ch;
				fenceLen = bar.length;
				continue;
			}
		}
		const rd = /^ {0,3}\[([^\]]+)\]:[ \t]*(.+)$/.exec(line);
		const label = rd?.[1];
		if (label !== undefined) set.add(normalizeLabel(label));
	}
	return set;
}

// The structural fingerprint, plus the defect (if any) that makes it
// untrustworthy. Both defects are UNTERMINATED load-bearing constructs, and
// both fail the same way: the opener's own text stops being fingerprinted, so
// an edit inside it produces an identical fingerprint and would pass.
export function markdownScan(text: string): MarkdownScan {
	const lines = text.replace(/\r\n?/g, "\n").split("\n");
	const items: MarkdownItem[] = [];
	const refLabels = collectRefLabels(lines);
	let defect: MarkdownDefect | null = null;

	let inComment = false;
	let commentBuf = "";

	const finishComment = (): void => {
		const inner = commentBuf.trim();
		if (DIRECTIVE.test(inner))
			items.push({ kind: "directive", value: `<!-- ${inner} -->` });
		commentBuf = "";
		inComment = false;
	};
	const htmlTagAt = (line: string, from: number): string | null => {
		if (!/^<\/?[A-Za-z]/.test(line.slice(from))) return null;
		let quote: '"' | "'" | null = null;
		for (let j = from + 1; j < line.length; j += 1) {
			const ch = line.charAt(j);
			if (quote !== null) {
				if (ch === quote) quote = null;
				continue;
			}
			if (ch === '"' || ch === "'") quote = ch;
			else if (ch === ">") return line.slice(from, j + 1);
		}
		return null;
	};

	const inlineScan = (line: string): void => {
		let i = 0;
		while (i < line.length) {
			if (inComment) {
				const end = line.indexOf("-->", i);
				if (end === -1) {
					commentBuf += `${line.slice(i)}\n`;
					return;
				}
				commentBuf += line.slice(i, end);
				finishComment();
				i = end + 3;
				continue;
			}
			if (
				line
					.slice(i)
					.match(/^<(?:script|style)\b[^>]*>[\s\S]*<\/(?:script|style)>/i)
			) {
				const body = line
					.slice(i)
					.match(/^<((?:script|style)\b[^>]*>[\s\S]*<\/(?:script|style)>)/i);
				if (body) {
					items.push({ kind: "raw-html", value: body[0] });
					i += body[0].length;
					continue;
				}
			}
			const tag = htmlTagAt(line, i);
			if (tag !== null) {
				items.push({ kind: "raw-html", value: tag });
				i += tag.length;
				continue;
			}
			const c = line.charAt(i);
			if (c === "`") {
				const run = backtickRun(line, i);
				const close = findCodeClose(line, i + run, run);
				if (close !== -1) {
					items.push({ kind: "code-span", value: line.slice(i + run, close) });
					i = close + run;
					continue;
				}
				i += run;
				continue;
			}
			if (c === "<") {
				if (line.startsWith("<!--", i)) {
					inComment = true;
					i += 4;
					continue;
				}
				const auto = matchAutolink(line, i);
				if (auto) {
					items.push({ kind: "autolink", value: auto.uri });
					i = auto.end;
					continue;
				}
				i += 1;
				continue;
			}
			if (c === "[" || (c === "!" && line.charAt(i + 1) === "[")) {
				const open = c === "!" ? i + 1 : i;
				const link = parseLink(line, open, refLabels);
				if (link) {
					items.push({ kind: link.kind, value: link.value });
					i = link.end;
					continue;
				}
			}
			// A bare `https://…` is a real destination even without `<>` or `[]()`
			// around it, so a sweep rewording its line must not alter it.
			const bare = matchBareUrl(line, i);
			if (bare) {
				items.push({ kind: "autolink", value: bare.uri });
				i = bare.end;
				continue;
			}
			i += 1;
		}
	};

	let inFence = false;
	let fenceChar = "";
	let fenceLen = 0;
	let fenceInfo = "";
	let fenceBody: string[] = [];
	let prevBlank = true;
	let listActive = false;

	let i = 0;
	// Front matter is a `---` fence only as the very first line of the file.
	const first = lines[0];
	if (first !== undefined && FRONT_OPEN.test(first)) {
		let j = 1;
		while (j < lines.length && !FRONT_CLOSE.test(lines[j] ?? "")) j += 1;
		if (j < lines.length) {
			items.push({ kind: "front-matter", value: lines.slice(1, j).join("\n") });
			i = j + 1;
		} else {
			// A leading `---` that never closes. Falling through treats the whole
			// YAML block as ordinary prose, which is exactly where a sweep may
			// reword freely — so an edit to a `title:` or a `draft:` flag would
			// pass. The document's structure is not placeable; say so.
			defect = "front matter opened with `---` and never closed";
		}
	}

	const flushFence = (closed: boolean): void => {
		if (!closed) {
			if (defect === null)
				defect = "a fenced code block opened and never closed";
			return;
		}
		items.push({
			kind: "fenced-code",
			value: `${fenceChar}${fenceLen}[${fenceInfo}]\n${fenceBody.join("\n")}`,
		});
		inFence = false;
		fenceBody = [];
	};

	for (; i < lines.length; i += 1) {
		const line = lines[i];
		if (line === undefined) break;

		if (inFence) {
			const m = FENCE_CLOSE.exec(line);
			const bar = m?.[1];
			if (bar && bar.charAt(0) === fenceChar && bar.length >= fenceLen)
				flushFence(true);
			else fenceBody.push(line);
			prevBlank = false;
			continue;
		}

		// Mid-comment: nothing else parses until the `-->` lands.
		if (inComment) {
			inlineScan(line);
			prevBlank = false;
			continue;
		}

		const fo = FENCE_OPEN.exec(line);
		const bar = fo?.[1];
		if (bar) {
			const ch = bar.charAt(0);
			const info = (fo?.[2] ?? "").trim();
			// A backtick info string cannot hold a backtick, else it is not a fence.
			if (!(ch === "`" && info.includes("`"))) {
				inFence = true;
				fenceChar = ch;
				fenceLen = bar.length;
				fenceInfo = info;
				fenceBody = [];
				prevBlank = false;
				continue;
			}
		}

		if (/^\s*$/.test(line)) {
			prevBlank = true;
			continue;
		}

		const atx = /^ {0,3}(#{1,6})(?:[ \t]+(.*))?$/.exec(line);
		const hashes = atx?.[1];
		if (hashes) {
			const raw = (atx?.[2] ?? "").replace(/[ \t]+#+[ \t]*$/, "").trim();
			items.push({ kind: "heading", value: `H${hashes.length} ${raw}` });
			prevBlank = false;
			listActive = false;
			continue;
		}

		const next = lines[i + 1];

		// A table: header row + delimiter row. A bare `---` with no pipe is a
		// Setext underline, resolved below, not a single-column table.
		if (
			next !== undefined &&
			isAlignmentRow(next) &&
			(next.includes("|") || line.includes("|"))
		) {
			items.push({ kind: "table-header", value: line.trim() });
			items.push({ kind: "table-delimiter", value: next.trim() });
			i += 1;
			prevBlank = false;
			continue;
		}

		const blockquote = /^ {0,3}(>+)[ \t]?/.exec(line)?.[1];
		const thematic = /^ {0,3}(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$/.exec(
			line,
		)?.[0];
		// Setext heading: paragraph text underlined with `===` (h1) or `---` (h2).
		if (
			next !== undefined &&
			/^ {0,3}(=+|-+)[ \t]*$/.test(next) &&
			isParagraph(line)
		) {
			const level = next.trim().startsWith("=") ? 1 : 2;
			items.push({ kind: "heading", value: `H${level} ${line.trim()}` });
			i += 1;
			prevBlank = false;
			listActive = false;
			continue;
		}
		if (blockquote !== undefined) {
			items.push({ kind: "block-marker", value: `blockquote:${blockquote}` });
			inlineScan(line);
			prevBlank = false;
			continue;
		}
		if (thematic !== undefined) {
			items.push({
				kind: "block-marker",
				value: `thematic:${thematic.trim()}`,
			});
			prevBlank = false;
			listActive = false;
			continue;
		}

		const rd = /^ {0,3}\[([^\]]+)\]:[ \t]*(.+)$/.exec(line);
		const rdLabel = rd?.[1];
		const rdRest = rd?.[2];
		if (rdLabel !== undefined && rdRest !== undefined) {
			items.push({
				kind: "ref-def",
				value: `${normalizeLabel(rdLabel)} -> ${refDefTarget(rdRest)}`,
			});
			prevBlank = false;
			continue;
		}

		// A list marker: track it so 4-space list continuation is not mistaken
		// for an indented code block below.
		const listMarker = /^ {0,3}([-*+]|\d{1,9}[.)])([ \t]|$)/.exec(line)?.[1];
		if (listMarker !== undefined) {
			items.push({ kind: "block-marker", value: `list:${listMarker}` });
			listActive = true;
			inlineScan(line);
			prevBlank = false;
			continue;
		}

		// Indented code block: 4+ spaces, preceded by a blank line, outside a
		// list. Its content is copy-pasteable code, so it is load-bearing.
		if (prevBlank && !listActive && indentWidth(line) >= 4) {
			const body: string[] = [line];
			while (i + 1 < lines.length) {
				const nx = lines[i + 1];
				if (nx === undefined) break;
				if (/^\s*$/.test(nx)) {
					let k = i + 1;
					while (k < lines.length && /^\s*$/.test(lines[k] ?? "")) k += 1;
					const cont = lines[k];
					if (cont !== undefined && indentWidth(cont) >= 4) {
						for (let b = i + 1; b <= k; b += 1) body.push(lines[b] ?? "");
						i = k;
						continue;
					}
					break;
				}
				if (indentWidth(nx) >= 4) {
					body.push(nx);
					i += 1;
					continue;
				}
				break;
			}
			items.push({ kind: "indented-code", value: body.join("\n") });
			prevBlank = false;
			continue;
		}

		if (indentWidth(line) === 0) listActive = false;
		inlineScan(line);
		prevBlank = false;
	}

	if (inFence) flushFence(false);
	// An open HTML comment at EOF never reaches finishComment, so directives in
	// it would fingerprint as nothing. Record the defect unless an earlier
	// front-matter defect already won; either makes the file fail closed.
	if (inComment && defect === null) {
		defect = "an HTML comment opened and never closed";
	}
	return { items, defect };
}

// The structural fingerprint alone. Exposed for tests and callers that have
// already established the document is well-formed; the gate goes through
// `compareMarkdown`, which cannot ignore a defect.
export function markdownFingerprint(text: string): MarkdownItem[] {
	return markdownScan(text).items;
}

// Compare two documents by structural fingerprint. A malformed side is
// reported BEFORE any comparison: two malformed sides can fingerprint
// identically while an edit hides inside the construct neither of them
// placed, so "equal" is not an answer the gate may give there.
export function compareMarkdown(
	before: string,
	after: string,
): MarkdownVerdict {
	const a = markdownScan(before);
	const b = markdownScan(after);
	if (a.defect) return { kind: "malformed", side: "before", reason: a.defect };
	if (b.defect) return { kind: "malformed", side: "after", reason: b.defect };
	const len = Math.max(a.items.length, b.items.length);
	for (let i = 0; i < len; i += 1) {
		const x = a.items[i] ?? null;
		const y = b.items[i] ?? null;
		if (x === null || y === null || x.kind !== y.kind || x.value !== y.value) {
			return { kind: "mismatch", index: i, before: x, after: y };
		}
	}
	return { kind: "same" };
}
