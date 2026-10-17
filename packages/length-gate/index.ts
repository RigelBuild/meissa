import {
	type CommentBlock,
	commentBlocks,
	extOf,
	type FileDiff,
} from "@rigelbuild/comment-scan";
import { diffArrays } from "diff";
import type { Heading, Nodes as MarkdownNode, Root } from "mdast";
import { fromMarkdown } from "mdast-util-from-markdown";
import { frontmatterFromMarkdown } from "mdast-util-frontmatter";
import { gfmFromMarkdown } from "mdast-util-gfm";
import { frontmatter } from "micromark-extension-frontmatter";
import { gfm } from "micromark-extension-gfm";

export const CEILING = 4;
export const MARKER = "LENGTH";
export const MIN_REASON_WORDS = 3;

export type DocClass = {
	name: string;
	include: readonly string[];
	exclude: readonly string[];
	target: number;
	markerAllowed: boolean;
};

export type LengthGateConfig = {
	extensions: readonly string[];
	exclude: readonly string[];
	docs: readonly DocClass[];
};

export type Finding =
	| {
			kind: "comment";
			path: string;
			line: number;
			lines: number;
			baseLines: number;
	  }
	| {
			kind: "doc";
			path: string;
			docClass: string;
			words: number;
			baseWords: number;
			target: number;
	  }
	| { kind: "bad-marker"; path: string; line: number; reason: string }
	| { kind: "unscannable"; path: string };

export type Note = { kind: "unneeded-marker"; path: string; line: number };

type ParsedCommentMarker = { reason: string };
type ParsedDocMarker = { section: string; reason: string };
type CommentScan = { blocks: CommentBlock[]; ambiguous: boolean };

const COMMENT_PREFIX = /^\s*(?:\/\/+|#+|\/\*+|<!--|--|;|%)\s*/u;
const GENERATED_HEADER = /Code generated\b.*DO NOT EDIT\.|@generated\b/u;

function wordsInReason(reason: string): number {
	return reason
		.trim()
		.split(/\s+/u)
		.filter((word) => /[\p{L}\p{N}]/u.test(word)).length;
}

function invalidReason(reason: string): string | null {
	if (wordsInReason(reason) < MIN_REASON_WORDS)
		return `reason must contain at least ${MIN_REASON_WORDS} words`;
	return null;
}

export function commentMarker(
	firstLine: string,
): ParsedCommentMarker | { error: string } | null {
	const prefix = COMMENT_PREFIX.exec(firstLine);
	if (!prefix) return null;
	let content = firstLine.slice(prefix[0].length).trim();
	if (!/^LENGTH\b/u.test(content)) return null;
	if (!/^LENGTH\s*:/u.test(content)) return null;
	content = content.replace(/^LENGTH\s*:/u, "");
	content = content.replace(/(?:\*\/|-->)\s*$/u, "").trim();
	const error = invalidReason(content);
	return error ? { error } : { reason: content };
}

const DOC_MARKER_LINE = /^<!--\s*LENGTH\s+§([^:]+:.*?)\s*-->$/u;
const HEADING_ID = /\s+\{#[^}]+\}\s*$/u;

function parseMarkdown(markdown: string): Root {
	return fromMarkdown(markdown, {
		extensions: [gfm(), frontmatter(["yaml"])],
		mdastExtensions: [gfmFromMarkdown(), frontmatterFromMarkdown(["yaml"])],
	});
}

function childrenOf(node: MarkdownNode): readonly MarkdownNode[] {
	if ("children" in node && Array.isArray(node.children)) return node.children;
	return [];
}

function headingText(node: MarkdownNode): string {
	if (node.type === "text" || node.type === "inlineCode") return node.value;
	if (node.type === "image" || node.type === "imageReference")
		return node.alt ?? "";
	return childrenOf(node).map(headingText).join("");
}

function headingsIn(root: Root): Heading[] {
	const headings: Heading[] = [];
	const visit = (node: MarkdownNode) => {
		if (node.type === "heading") headings.push(node);
		for (const child of childrenOf(node)) visit(child);
	};
	visit(root);
	return headings;
}

function markerNodes(root: Root): Array<{
	node: Extract<MarkdownNode, { type: "html" }>;
	line: number;
}> {
	const markers: Array<{
		node: Extract<MarkdownNode, { type: "html" }>;
		line: number;
	}> = [];
	for (const node of root.children) {
		if (node.type !== "html") continue;
		const value = node.value.trim();
		if (!/^<!--\s*LENGTH\b/u.test(value)) continue;
		const line = node.position?.start.line;
		if (line !== undefined) markers.push({ node, line });
	}
	return markers;
}

function normalizeHeading(heading: string): string {
	return heading
		.replace(HEADING_ID, "")
		.replace(/^§/u, "")
		.replace(/`/gu, "")
		.replace(/\s+/gu, " ")
		.trim()
		.toLocaleLowerCase("en-US");
}

// HTML comments and script/style bodies are not prose; other tags are stripped to their text.
function htmlText(html: string): string {
	return html
		.replace(/<!--[\s\S]*?(?:-->|$)/gu, " ")
		.replace(/<(script|style)\b[\s\S]*?(?:<\/\1\s*>|$)/giu, " ")
		.replace(/<[^>]*>/gu, " ");
}

function docMarkerFromTree(
	root: Root,
): ParsedDocMarker | { error: string } | null {
	const markers = markerNodes(root);
	if (markers.length === 0) return null;
	if (markers.length > 1)
		return { error: "only one doc LENGTH marker is allowed" };
	const marker = markers[0];
	if (!marker) return null;
	const markerValue = marker.node.value.trim();

	const headings = headingsIn(root).filter((node) => node.depth === 2);
	const firstHeading = headings[0];
	const yamlEnd = root.children.reduce((end, node) => {
		if (node.type !== "yaml") return end;
		return Math.max(end, node.position?.end.line ?? 0);
	}, 0);
	if (
		marker.line <= yamlEnd ||
		firstHeading === undefined ||
		(firstHeading.position?.start.line ?? Number.POSITIVE_INFINITY) <=
			marker.line
	)
		return {
			error:
				"doc marker must be after front matter, before the first ## heading, and name an existing section",
		};

	if (markerValue.includes("\n") || markerValue.includes("\r"))
		return { error: "doc LENGTH marker must be one line" };
	const parsed = DOC_MARKER_LINE.exec(markerValue);
	if (!parsed)
		return {
			error:
				"doc marker must be one HTML comment line: <!-- LENGTH §Section: reason -->",
		};
	// A heading may itself contain ":", so split at the last colon that ends a real heading.
	const body = parsed[1] ?? "";
	const names = new Set(
		headings.map((node) => normalizeHeading(headingText(node))),
	);
	let split = body.indexOf(":");
	for (let at = split; at !== -1; at = body.indexOf(":", at + 1))
		if (names.has(normalizeHeading(body.slice(0, at)))) split = at;
	const section = body.slice(0, split).trim();
	const reason = body.slice(split + 1).trim();
	const reasonError = invalidReason(reason);
	if (reasonError) return { error: reasonError };
	if (!section || !names.has(normalizeHeading(section)))
		return {
			error: `doc marker section §${section} does not match an existing ## heading`,
		};
	return { section, reason };
}

function inlineText(node: MarkdownNode): string {
	if (node.type === "text" || node.type === "inlineCode") return node.value;
	if (node.type === "link" && node.children.length === 1) {
		const only = node.children[0];
		if (only?.type === "text" && only.value === node.url) return "";
	}
	if (node.type === "link" || node.type === "linkReference")
		return childrenOf(node).map(inlineText).join("");
	if (node.type === "html") return ` ${htmlText(node.value)} `;
	if (node.type === "footnoteReference") return "";
	if (node.type === "image" || node.type === "imageReference")
		return node.alt ?? "";
	if (node.type === "break") return " ";
	return childrenOf(node).map(inlineText).join("");
}

function proseBlocks(root: Root, dropErrata: boolean): string[] {
	const blocks: string[] = [];
	let inErrata = false;
	const visit = (node: MarkdownNode) => {
		if (
			node.type === "yaml" ||
			node.type === "code" ||
			node.type === "table" ||
			node.type === "definition"
		)
			return;
		if (node.type === "heading") {
			if (dropErrata && root.children.includes(node) && node.depth <= 2)
				inErrata = false;
			if (
				dropErrata &&
				node.depth === 2 &&
				root.children.includes(node) &&
				normalizeHeading(headingText(node)) === "errata"
			) {
				inErrata = true;
				return;
			}
		}
		if (dropErrata && inErrata) return;
		if (node.type === "heading" || node.type === "paragraph") {
			blocks.push(childrenOf(node).map(inlineText).join(""));
			return;
		}
		if (node.type === "html") {
			const text = htmlText(node.value).trim();
			if (text) blocks.push(text);
			return;
		}
		for (const child of childrenOf(node)) visit(child);
	};
	visit(root);
	return blocks;
}

function countProseWords(root: Root, dropErrata: boolean): number {
	return proseBlocks(root, dropErrata)
		.join("\n")
		.split(/\s+/u)
		.filter((token) => /[\p{L}\p{N}]/u.test(token)).length;
}

export function docMarker(
	text: string,
): ParsedDocMarker | { error: string } | null {
	return docMarkerFromTree(parseMarkdown(text));
}

export function proseWords(markdown: string, dropErrata: boolean): number {
	return countProseWords(parseMarkdown(markdown), dropErrata);
}

// Strips comment tokens so a moved line pairs on its text; "" when it holds no letter or digit.
function fallbackCommentKey(line: string): string {
	const content = line.replace(/^\s*(?:(?:\/\/|#|\*|--|;|\/\*|\*\/)\s*)+/u, "");
	return /[\p{L}\p{N}]/u.test(content) ? content : "";
}

export function lineMap(
	before: string,
	after: string,
): (headLine: number) => number | null {
	const beforeLines = before
		.replace(/\r\n?/gu, "\n")
		.split("\n")
		.map((line) => line.trim());
	const afterLines = after
		.replace(/\r\n?/gu, "\n")
		.split("\n")
		.map((line) => line.trim());
	const mapping = new Map<number, number>();
	const mappedBefore = new Set<number>();
	const mappedAfter = new Set<number>();
	let beforeLine = 1;
	let afterLine = 1;
	for (const change of diffArrays(beforeLines, afterLines)) {
		if (change.added) {
			afterLine += change.value.length;
		} else if (change.removed) {
			beforeLine += change.value.length;
		} else {
			for (let offset = 0; offset < change.value.length; offset++) {
				const oldLine = beforeLine + offset;
				const newLine = afterLine + offset;
				mapping.set(newLine, oldLine);
				mappedBefore.add(oldLine);
				mappedAfter.add(newLine);
			}
			beforeLine += change.value.length;
			afterLine += change.value.length;
		}
	}
	const beforeCounts = new Map<string, { count: number; line: number }>();
	for (let index = 0; index < beforeLines.length; index++) {
		const line = index + 1;
		const text = beforeLines[index] ?? "";
		if (mappedBefore.has(line) || text === "") continue;
		const key = fallbackCommentKey(text);
		if (key === "") continue;
		const entry = beforeCounts.get(key);
		beforeCounts.set(key, { count: (entry?.count ?? 0) + 1, line });
	}
	const afterCounts = new Map<string, { count: number; line: number }>();
	for (let index = 0; index < afterLines.length; index++) {
		const line = index + 1;
		const text = afterLines[index] ?? "";
		if (mappedAfter.has(line) || text === "") continue;
		const key = fallbackCommentKey(text);
		if (key === "") continue;
		const entry = afterCounts.get(key);
		afterCounts.set(key, { count: (entry?.count ?? 0) + 1, line });
	}
	for (const [text, base] of beforeCounts) {
		const head = afterCounts.get(text);
		if (base.count === 1 && head?.count === 1)
			mapping.set(head.line, base.line);
	}
	return (headLine) => mapping.get(headLine) ?? null;
}

function matchesAny(path: string, globs: readonly string[]): boolean {
	return globs.some((glob) => new Bun.Glob(glob).match(path));
}

function docClassFor(
	path: string,
	classes: readonly DocClass[],
): DocClass | null {
	for (const docClass of classes) {
		if (
			matchesAny(path, docClass.include) &&
			!matchesAny(path, docClass.exclude)
		)
			return docClass;
	}
	return null;
}

function isGeneratedHeader(
	text: string,
	blocks: readonly CommentBlock[],
): boolean {
	const lines = text.replace(/\r\n?/gu, "\n").split("\n");
	for (const block of blocks) {
		if (!block.doc || block.start > 5) continue;
		const start = block.start - 1;
		const end = Math.min(5, start + block.lines);
		if (
			lines.slice(start, end).some((line) => GENERATED_HEADER.test(line ?? ""))
		)
			return true;
	}
	return false;
}

function changedHeadLines(file: FileDiff): Set<number> {
	const changed = new Set<number>();
	for (const hunk of file.hunks) {
		for (const line of hunk.lines) {
			if (line.kind === "+") changed.add(line.newNo);
		}
	}
	return changed;
}

function fileExcess(
	blocks: readonly CommentBlock[],
	liveMarkers: ReadonlySet<number>,
): number {
	let excess = 0;
	for (const block of blocks) {
		if (block.doc || liveMarkers.has(block.start)) continue;
		excess += Math.max(0, block.lines - CEILING);
	}
	return excess;
}

function scanSource(
	path: string,
	text: string,
	extension: string,
): CommentScan | Finding {
	const result = commentBlocks(text, extension);
	if (result === null || result.ambiguous) return { kind: "unscannable", path };
	return result;
}

export function judgeFile(
	config: LengthGateConfig,
	file: FileDiff,
	texts: { before: string; after: string },
): { findings: Finding[]; notes: Note[] } {
	const findings: Finding[] = [];
	const notes: Note[] = [];
	if (
		file.deleted ||
		(file.metadataOnly &&
			file.hunks.length === 0 &&
			file.oldPath === file.newPath) ||
		matchesAny(file.path, config.exclude)
	)
		return { findings, notes };

	const extension = extOf(file.path);
	const scanComments = config.extensions.includes(extension);
	const docClass = docClassFor(file.path, config.docs);
	if (!scanComments && docClass === null) return { findings, notes };
	if (file.binary)
		return { findings: [{ kind: "unscannable", path: file.path }], notes };

	let beforeBlocks: CommentBlock[] = [];
	let afterBlocks: CommentBlock[] = [];
	if (scanComments) {
		const beforeText = file.oldPath === file.newPath ? texts.before : "";
		const after = scanSource(file.path, texts.after, extension);
		if ("kind" in after) return { findings: [after], notes };
		afterBlocks = after.blocks;
		if (isGeneratedHeader(texts.after, after.blocks))
			return { findings, notes };

		const before = scanSource(file.path, beforeText, extension);
		beforeBlocks = "kind" in before ? [] : before.blocks;
	}

	const addedLines = changedHeadLines(file);
	if (scanComments) {
		const liveMarkers = new Set<number>();
		for (const block of afterBlocks) {
			if (block.doc || !addedLines.has(block.start)) continue;
			const marker = commentMarker(block.first);
			if (marker === null) continue;
			if ("error" in marker) {
				findings.push({
					kind: "bad-marker",
					path: file.path,
					line: block.start,
					reason: marker.error,
				});
				continue;
			}
			liveMarkers.add(block.start);
			if (block.lines <= CEILING)
				notes.push({
					kind: "unneeded-marker",
					path: file.path,
					line: block.start,
				});
		}

		if (
			fileExcess(afterBlocks, liveMarkers) > fileExcess(beforeBlocks, new Set())
		) {
			const lineMapping = lineMap(texts.before, texts.after);
			for (const block of afterBlocks) {
				if (block.doc || block.lines <= CEILING || liveMarkers.has(block.start))
					continue;
				let baseLines = 0;
				for (let line = block.start; line < block.start + block.lines; line++) {
					const baseLine = lineMapping(line);
					if (baseLine === null) continue;
					for (const baseBlock of beforeBlocks) {
						if (
							baseBlock.doc ||
							baseLine < baseBlock.start ||
							baseLine >= baseBlock.start + baseBlock.lines
						)
							continue;
						baseLines = Math.max(baseLines, baseBlock.lines);
					}
				}
				if (block.lines > baseLines)
					findings.push({
						kind: "comment",
						path: file.path,
						line: block.start,
						lines: block.lines,
						baseLines,
					});
			}
		}
	}

	const beforeText = file.oldPath === file.newPath ? texts.before : "";
	if (docClass !== null && beforeText !== texts.after) {
		const parsedAfter = parseMarkdown(texts.after);
		const markerLines = markerNodes(parsedAfter).map(({ line }) => line);
		const addedMarker = markerLines.find((line) => addedLines.has(line));
		const markerLine = addedMarker ?? markerLines[0] ?? -1;
		let liveDocMarker: ParsedDocMarker | null = null;
		const liveMarker = addedMarker !== undefined;
		if (liveMarker && !docClass.markerAllowed) {
			findings.push({
				kind: "bad-marker",
				path: file.path,
				line: markerLine,
				reason: "LENGTH markers are not allowed for this doc class",
			});
		} else if (liveMarker) {
			const marker = docMarkerFromTree(parsedAfter);
			if (marker === null || "error" in marker) {
				findings.push({
					kind: "bad-marker",
					path: file.path,
					line: markerLine,
					reason: marker === null ? "invalid doc LENGTH marker" : marker.error,
				});
			} else {
				liveDocMarker = marker;
			}
		}
		const wordsAfter = countProseWords(
			parsedAfter,
			docClass.name === "design record",
		);
		const wordsBefore = countProseWords(
			parseMarkdown(beforeText),
			docClass.name === "design record",
		);
		if (liveDocMarker !== null && wordsAfter <= docClass.target)
			notes.push({
				kind: "unneeded-marker",
				path: file.path,
				line: markerLine,
			});
		if (
			wordsAfter > docClass.target &&
			wordsAfter > wordsBefore &&
			liveDocMarker === null
		)
			findings.push({
				kind: "doc",
				path: file.path,
				docClass: docClass.name,
				words: wordsAfter,
				baseWords: wordsBefore,
				target: docClass.target,
			});
	}

	return { findings, notes };
}

export function formatFinding(
	finding: Finding,
	config: LengthGateConfig,
): string[] {
	switch (finding.kind) {
		case "comment":
			return [
				`${finding.path}:${finding.line}: ${finding.lines}-line comment block (ceiling ${CEILING})`,
				`Trim it to ${CEILING} lines, or add a LENGTH marker with a reason.`,
			];
		case "doc": {
			const markerAllowed =
				config.docs.find(({ name }) => name === finding.docClass)
					?.markerAllowed ?? false;
			return [
				`${finding.path}: ${finding.words.toLocaleString("en-US")} prose words (${finding.docClass} target ${finding.target.toLocaleString("en-US")})`,
				markerAllowed
					? "Trim it to the target, or add a LENGTH marker naming the section that carries the excess."
					: "Split the document into a directory of files.",
			];
		}
		case "bad-marker":
			return [
				`${finding.path}:${finding.line}: bad LENGTH marker: ${finding.reason}`,
				"Fix the reason or § section, or remove the marker.",
			];
		case "unscannable":
			return [
				`${finding.path}: no scanner could place comments`,
				"Fix the unclosed construct reported by the scanner, or add scanner support.",
			];
	}
}

export type { FileDiff };
