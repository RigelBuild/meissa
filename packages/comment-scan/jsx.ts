// `.tsx`/`.jsx` comment location, via the `typescript` compiler this project
// already installs. A char scanner cannot do this: in `<a>x//y</a>` the `//`
// is JSX text, and stripping from there hides the rest of the line. The parser
// finds comment RANGES only; the verdict is still residue equality.

import ts from "typescript";
import { TS_DIRECTIVE } from "./languages.ts";

type Span = { pos: number; end: number };

// `parseDiagnostics` is real on every SourceFile but @internal in the public
// .d.ts, so it is read through this widening rather than an `as` cast.
type ParsedSource = ts.SourceFile & {
	parseDiagnostics?: readonly unknown[];
};

// Every `[pos, end)` a comment could hide behind. `getLeadingCommentRanges` is
// LEXICAL and has no idea it is inside JSX: at the pos of a JsxText node
// beginning `//` it returns a bogus range (`<a>//not a comment</a>` yields the
// whole tail). Discarding ranges inside a JsxText span is what fixes that.
function jsxTextSpans(source: ts.SourceFile): Span[] {
	const spans: Span[] = [];
	const walk = (node: ts.Node): void => {
		if (node.kind === ts.SyntaxKind.JsxText) {
			spans.push({ pos: node.getFullStart(), end: node.end });
		}
		ts.forEachChild(node, walk);
	};
	walk(source);
	return spans;
}

function commentRanges(source: ts.SourceFile, text: string): Span[] {
	const jsx = jsxTextSpans(source);
	const seen = new Set<number>();
	const ranges: Span[] = [];
	const add = (found: readonly ts.CommentRange[] | undefined): void => {
		for (const range of found ?? []) {
			if (seen.has(range.pos)) continue;
			if (jsx.some((span) => range.pos >= span.pos && range.pos < span.end)) {
				continue;
			}
			seen.add(range.pos);
			ranges.push({ pos: range.pos, end: range.end });
		}
	};
	// `getChildren`, not `forEachChild`: a comment attaches to the nearest
	// TOKEN, and a `{/* c */}` expression has no child NODE at all — its comment
	// is trailing trivia on the `{` token. A node-only walk misses it, and the
	// comment then survives into the residue where a reword reads as code.
	const walk = (node: ts.Node): void => {
		add(ts.getLeadingCommentRanges(text, node.pos));
		add(ts.getTrailingCommentRanges(text, node.end));
		for (const child of node.getChildren(source)) walk(child);
	};
	walk(source);
	return ranges;
}

// Remove every non-directive comment range, then split into lines. A span is
// DELETED, not blanked: blanking makes a comment's WIDTH load-bearing, so
// rewording an inline `/* c */` would shift the residue and read as code. Its
// NEWLINES are kept — the residue is indexed by line for the offender report.
export function scanJsx(text: string): {
	residues: string[];
	ambiguous: boolean;
} {
	const source: ParsedSource = ts.createSourceFile(
		"input.tsx",
		text,
		ts.ScriptTarget.Latest,
		true,
		ts.ScriptKind.TSX,
	);
	// A file that does not parse cannot have its comment positions trusted, so
	// it fails closed rather than being classified from a broken tree.
	if ((source.parseDiagnostics ?? []).length > 0) {
		return { residues: text.split("\n"), ambiguous: true };
	}

	// Ranges arrive in walk order, which is not source order; sort so the
	// rebuild below can stream the text once.
	const ranges = commentRanges(source, text)
		.filter((range) => {
			// A directive is read by the toolchain, so it stays in the residue and
			// a sweep cannot rewrite it silently. `TS_DIRECTIVE` matches the line
			// AND the block form, which is the only form available inside JSX.
			return !TS_DIRECTIVE.test(text.slice(range.pos, range.end));
		})
		.sort((a, b) => a.pos - b.pos);

	let out = "";
	let at = 0;
	for (const range of ranges) {
		if (range.pos < at) continue; // nested/duplicate range already consumed
		out += text.slice(at, range.pos);
		const newlines = text.slice(range.pos, range.end).match(/\n/g);
		if (newlines) out += newlines.join("");
		at = range.end;
	}
	out += text.slice(at);

	return {
		residues: out.split("\n").map((line) => line.replace(/[ \t]+$/, "")),
		ambiguous: false,
	};
}
