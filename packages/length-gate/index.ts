import {
	type CommentBlock,
	commentBlocks,
	extOf,
	type FileDiff,
} from "@rigelbuild/comment-scan";
import { diffArrays } from "diff";

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
const DOC_MARKER_START = /<!--\s*LENGTH\b/u;
const DOC_MARKER_LINE = /^\s*<!--\s*LENGTH\s+§([^:]+):\s*(.*?)\s*-->\s*$/u;
const H2_HEADING = /^ {0,3}##(?!#)\s+(.+?)\s*#*\s*$/u;
const TABLE_SEPARATOR = /^\s*\|?\s*:?-{3,}:?(?:\s*\|\s*:?-{3,}:?)*\s*\|?\s*$/u;

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
	if (!/^LENGTH\s*:/u.test(content))
		return { error: "marker must use LENGTH: <reason>" };
	content = content.replace(/^LENGTH\s*:/u, "");
	content = content.replace(/(?:\*\/|-->)\s*$/u, "").trim();
	const error = invalidReason(content);
	return error ? { error } : { reason: content };
}

function frontMatterEnd(lines: readonly string[]): number {
	if (lines[0]?.replace(/^\uFEFF/u, "").trim() !== "---") return 0;
	for (let index = 1; index < lines.length; index++) {
		const line = lines[index]?.trim();
		if (line === "---" || line === "...") return index + 1;
	}
	return lines.length;
}

function markdownWithoutComments(markdown: string): string {
	const lines = markdown.replace(/\r\n?/gu, "\n").split("\n");
	const start = frontMatterEnd(lines);
	let fence: { char: string; length: number } | null = null;
	let inComment = false;
	for (let index = start; index < lines.length; index++) {
		const line = lines[index] ?? "";
		if (fence) {
			if (
				new RegExp(`^ {0,3}${fence.char}{${fence.length},}\\s*$`, "u").test(
					line,
				)
			)
				fence = null;
			continue;
		}
		const opening = /^ {0,3}(`{3,}|~{3,})/u.exec(line)?.[1];
		if (opening) {
			fence = { char: opening[0] ?? "`", length: opening.length };
			continue;
		}
		let visible = "";
		let offset = 0;
		while (offset < line.length) {
			if (inComment) {
				const end = line.indexOf("-->", offset);
				if (end === -1) break;
				offset = end + 3;
				inComment = false;
				continue;
			}
			const begin = line.indexOf("<!--", offset);
			if (begin === -1) {
				visible += line.slice(offset);
				break;
			}
			visible += line.slice(offset, begin);
			const end = line.indexOf("-->", begin + 4);
			if (end === -1) {
				inComment = true;
				break;
			}
			offset = end + 3;
		}
		lines[index] = visible;
	}
	return lines.join("\n");
}

function visibleMarkdownLines(markdown: string, dropErrata: boolean): string[] {
	const lines = markdown.replace(/\r\n?/gu, "\n").split("\n");
	const start = frontMatterEnd(lines);
	for (let index = 0; index < start; index++) lines[index] = "";

	let fence: { char: string; length: number } | null = null;
	let inComment = false;
	for (let index = start; index < lines.length; index++) {
		const line = lines[index] ?? "";
		if (fence) {
			lines[index] = "";
			if (
				new RegExp(`^ {0,3}${fence.char}{${fence.length},}\\s*$`, "u").test(
					line,
				)
			)
				fence = null;
			continue;
		}
		const opening = /^ {0,3}(`{3,}|~{3,})/u.exec(line)?.[1];
		if (opening) {
			fence = { char: opening[0] ?? "`", length: opening.length };
			lines[index] = "";
			continue;
		}

		let visible = "";
		let offset = 0;
		while (offset < line.length) {
			if (inComment) {
				const end = line.indexOf("-->", offset);
				if (end === -1) break;
				offset = end + 3;
				inComment = false;
				continue;
			}
			const commentStart = line.indexOf("<!--", offset);
			if (commentStart === -1) {
				visible += line.slice(offset);
				break;
			}
			visible += line.slice(offset, commentStart);
			const commentEnd = line.indexOf("-->", commentStart + 4);
			if (commentEnd === -1) {
				inComment = true;
				break;
			}
			offset = commentEnd + 3;
		}
		lines[index] = visible;
	}

	if (dropErrata) {
		const errata = lines.findIndex((line) =>
			/^ {0,3}##(?!#)\s+Errata\s*#*\s*$/u.test(line),
		);
		if (errata !== -1) {
			let end = lines.length;
			for (let index = errata + 1; index < lines.length; index++) {
				if (/^ {0,3}#{1,2}\s+/u.test(lines[index] ?? "")) {
					end = index;
					break;
				}
			}
			for (let index = errata; index < end; index++) lines[index] = "";
		}
	}

	for (let index = 0; index < lines.length; index++) {
		if (!TABLE_SEPARATOR.test(lines[index] ?? "")) continue;
		let tableStart = index;
		if (tableStart > 0 && (lines[tableStart - 1] ?? "").includes("|"))
			tableStart--;
		let tableEnd = index;
		while (
			tableEnd + 1 < lines.length &&
			(lines[tableEnd + 1] ?? "").trim() !== "" &&
			(lines[tableEnd + 1] ?? "").includes("|")
		)
			tableEnd++;
		for (let row = tableStart; row <= tableEnd; row++) lines[row] = "";
	}

	return lines.map((line) =>
		line
			.replace(/^\s{0,3}\[[^\]]+\]:\s*\S+.*$/u, "")
			.replace(
				/!?\[([^\]]+)\]\((?:<[^>]*>|[^)\s]+)(?:\s+["'][^"']*["'])?\)/gu,
				"$1",
			)
			.replace(/\[([^\]]+)\]\[[^\]]*\]/gu, "$1")
			.replace(/<(?:https?:\/\/|mailto:)[^>]+>/giu, ""),
	);
}

function docMarkerLines(lines: readonly string[]): number[] {
	const start = frontMatterEnd(lines);
	const markers: number[] = [];
	let fence: { char: string; length: number } | null = null;
	for (let index = start; index < lines.length; index++) {
		const line = lines[index] ?? "";
		if (fence) {
			if (
				new RegExp(`^ {0,3}${fence.char}{${fence.length},}\\s*$`, "u").test(
					line,
				)
			)
				fence = null;
			continue;
		}
		const opening = /^ {0,3}(`{3,}|~{3,})/u.exec(line)?.[1];
		if (opening) {
			fence = { char: opening[0] ?? "`", length: opening.length };
			continue;
		}
		if (DOC_MARKER_START.test(line)) markers.push(index);
	}
	return markers;
}
export function docMarker(
	text: string,
): ParsedDocMarker | { error: string } | null {
	const lines = text.replace(/\r\n?/gu, "\n").split("\n");
	const markerLines = docMarkerLines(lines);
	if (markerLines.length === 0) return null;
	if (markerLines.length > 1)
		return { error: "only one doc LENGTH marker is allowed" };

	const markerIndex = markerLines[0];
	if (markerIndex === undefined) return null;
	const start = frontMatterEnd(lines);
	const headings = visibleMarkdownLines(markdownWithoutComments(text), false)
		.map((line, index) => ({ match: H2_HEADING.exec(line), index }))
		.filter((entry) => entry.match !== null);
	const firstHeading = headings[0]?.index;
	if (
		markerIndex < start ||
		firstHeading === undefined ||
		markerIndex >= firstHeading
	)
		return {
			error:
				"doc marker must be before the first ## heading and name an existing section",
		};

	const marker = DOC_MARKER_LINE.exec(lines[markerIndex] ?? "");
	if (!marker)
		return {
			error:
				"doc marker must be one HTML comment line: <!-- LENGTH §Section: reason -->",
		};
	const section = marker[1]?.trim() ?? "";
	const reason = marker[2]?.trim() ?? "";
	const reasonError = invalidReason(reason);
	if (reasonError) return { error: reasonError };
	const normalizeHeading = (heading: string) =>
		heading
			.replace(/\s+\{#[^}]+\}\s*$/u, "")
			.replace(/^§/u, "")
			.replace(/\s+/gu, " ")
			.trim()
			.toLocaleLowerCase("en-US");
	const target = normalizeHeading(section);
	const exists = headings.some((entry) => {
		const title = entry.match?.[1];
		return title !== undefined && normalizeHeading(title) === target;
	});
	if (!section || !exists)
		return {
			error: `doc marker section §${section} does not match an existing ## heading`,
		};
	return { section, reason };
}

export function proseWords(markdown: string, dropErrata: boolean): number {
	return visibleMarkdownLines(markdown, dropErrata)
		.join("\n")
		.split(/\s+/u)
		.filter((token) => /[\p{L}\p{N}]/u.test(token)).length;
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
	let beforeLine = 1;
	let afterLine = 1;
	for (const change of diffArrays(beforeLines, afterLines)) {
		if (change.added) {
			afterLine += change.value.length;
		} else if (change.removed) {
			beforeLine += change.value.length;
		} else {
			for (let offset = 0; offset < change.value.length; offset++)
				mapping.set(afterLine + offset, beforeLine + offset);
			beforeLine += change.value.length;
			afterLine += change.value.length;
		}
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
		file.metadataOnly ||
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
		if ("kind" in before) return { findings: [before], notes };
		beforeBlocks = before.blocks;
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
		const lines = texts.after.replace(/\r\n?/gu, "\n").split("\n");
		const markerLines = docMarkerLines(lines);
		const addedMarker = markerLines.find((index) => addedLines.has(index + 1));
		const markerIndex = addedMarker ?? markerLines[0] ?? -1;
		let liveDocMarker: ParsedDocMarker | null = null;
		const liveMarker = addedMarker !== undefined;
		if (liveMarker && !docClass.markerAllowed) {
			findings.push({
				kind: "bad-marker",
				path: file.path,
				line: markerIndex + 1,
				reason: "LENGTH markers are not allowed for this doc class",
			});
		} else if (liveMarker) {
			const marker = docMarker(texts.after);
			if (marker === null || "error" in marker) {
				findings.push({
					kind: "bad-marker",
					path: file.path,
					line: markerIndex + 1,
					reason: marker === null ? "invalid doc LENGTH marker" : marker.error,
				});
			} else {
				liveDocMarker = marker;
			}
		}
		const wordsAfter = proseWords(
			texts.after,
			docClass.name === "design record",
		);
		const wordsBefore = proseWords(
			beforeText,
			docClass.name === "design record",
		);
		if (liveDocMarker !== null && wordsAfter <= docClass.target)
			notes.push({
				kind: "unneeded-marker",
				path: file.path,
				line: markerIndex + 1,
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
