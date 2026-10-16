import { type Residues, residueScannerFor } from "./diff.ts";
import { LANG_BY_EXT } from "./languages.ts";
import { scanComplete } from "./scan.ts";

export type CommentBlock = {
	start: number;
	lines: number;
	first: string;
	doc: boolean;
};

type ProtoScope = "message" | "service" | "other";
type GoGroup = "const" | "type" | "var";

function exportedName(name: string): boolean {
	const first = name.charAt(0);
	return (
		first !== "" &&
		first !== "_" &&
		first === first.toUpperCase() &&
		first !== first.toLowerCase()
	);
}

function declaredNames(text: string): string[] {
	const names =
		/^[\p{L}_][\p{L}\p{N}_]*(?:\s*,\s*[\p{L}_][\p{L}\p{N}_]*)*/u.exec(
			text.trim(),
		)?.[0];
	return names?.split(",").map((name) => name.trim()) ?? [];
}

function goSpecExported(line: string): boolean {
	return declaredNames(line).some(exportedName);
}

function groupHasExportedSpec(
	lines: readonly string[],
	start: number,
): boolean {
	let depth = 1;
	for (let i = start + 1; i < lines.length; i++) {
		const line = lines[i] ?? "";
		if (goSpecExported(line)) return true;
		for (const char of line) {
			if (char === "(") depth++;
			if (char === ")") depth--;
		}
		if (depth <= 0) return false;
	}
	return false;
}

function goDocAt(
	lines: readonly string[],
	lineNo: number,
	line: string,
): boolean {
	const text = line.trim();
	if (/^package\s+[\p{L}_][\p{L}\p{N}_]*\s*$/u.test(text)) return true;
	const fn = /^func\s+(?:\([^)]*\)\s*)?([\p{L}_][\p{L}\p{N}_]*)/u.exec(
		text,
	)?.[1];
	if (fn !== undefined) return exportedName(fn);
	const group = /^(const|type|var)\s*\(/.exec(text);
	if (group) return groupHasExportedSpec(lines, lineNo);
	const declaration = /^(?:const|type|var)\s+(.+)$/.exec(text)?.[1];
	return declaration !== undefined && goSpecExported(declaration);
}

function goGroupKinds(
	lines: readonly string[],
	residues: readonly string[],
): (GoGroup | null)[] {
	const groups: (GoGroup | null)[] = [];
	let active: GoGroup | null = null;
	for (let i = 0; i < lines.length; i++) {
		groups.push(active);
		const code = residues[i] ?? "";
		const opener = /^\s*(const|type|var)\s*\(/.exec(code)?.[1];
		if (active === null && opener !== undefined) active = opener as GoGroup;
		if (active !== null && /^\s*\)/.test(code)) active = null;
	}
	return groups;
}

function protoScopes(lines: readonly string[]): ProtoScope[][] {
	const scopes: ProtoScope[][] = [];
	const stack: ProtoScope[] = [];
	for (const line of lines) {
		scopes.push([...stack]);
		let quote = false;
		let escaped = false;
		for (let i = 0; i < line.length; i++) {
			const char = line[i] ?? "";
			if (quote) {
				if (escaped) escaped = false;
				else if (char === "\\") escaped = true;
				else if (char === '"') quote = false;
				continue;
			}
			if (char === '"') {
				quote = true;
				continue;
			}
			if (char === "}") stack.pop();
			if (char === "{") {
				const prefix = line.slice(0, i);
				const declaration = /\b(message|service)\s+[\w.]+\s*$/.exec(
					prefix,
				)?.[1];
				stack.push(
					declaration === "message"
						? "message"
						: declaration === "service"
							? "service"
							: "other",
				);
			}
		}
	}
	return scopes;
}

function protoDocAt(line: string, scopes: readonly ProtoScope[]): boolean {
	const text = line.trim();
	if (/^(?:message|enum|service)\s+[\w.]+\b/.test(text)) return true;
	if (scopes.includes("service") && /^rpc\s+[\w]+\s*\(/.test(text)) return true;
	return (
		scopes.includes("message") &&
		/^(?:(?:optional|required|repeated)\s+)?(?:map\s*<[^>]+>|[\w.]+)\s+[\w]+\s*=\s*\d+\b/.test(
			text,
		)
	);
}

function docMarker(first: string): boolean {
	const text = first.trimStart();
	return (
		text.startsWith("/**") ||
		text.startsWith("/*!") ||
		text.startsWith("///") ||
		text.startsWith("//!")
	);
}

function commentRuns(
	lines: readonly string[],
	residues: readonly string[],
): { start: number; lines: number }[] {
	const blocks: { start: number; lines: number }[] = [];
	for (let i = 0; i < lines.length; ) {
		if ((lines[i] ?? "").trim() === "" || (residues[i] ?? "").trim() !== "") {
			i++;
			continue;
		}
		const start = i;
		while (
			i < lines.length &&
			(lines[i] ?? "").trim() !== "" &&
			(residues[i] ?? "").trim() === ""
		)
			i++;
		blocks.push({ start, lines: i - start });
	}
	return blocks;
}

export function commentBlocks(
	text: string,
	ext: string,
): { blocks: CommentBlock[]; ambiguous: boolean } | null {
	const scanner = residueScannerFor(ext);
	if (scanner === null) return null;
	if (ext === ".go" && isCgo(text)) return { blocks: [], ambiguous: true };
	const scan: Residues = scanner(text);
	if (scan.ambiguous) return { blocks: [], ambiguous: true };
	const lines = text.split("\n");
	const generated = lines
		.slice(0, 5)
		.some((line) => /Code generated\b.*DO NOT EDIT\.|@generated\b/.test(line));
	const runs = commentRuns(lines, scan.residues);
	const goGroups = ext === ".go" ? goGroupKinds(lines, scan.residues) : [];
	const proto = ext === ".proto" ? protoScopes(scan.residues) : [];
	return {
		blocks: runs.map(({ start, lines: count }) => {
			const first = lines[start] ?? "";
			const nextIndex = start + count;
			const next = lines[nextIndex] ?? "";
			const attached =
				next.trim() !== "" && (scan.residues[nextIndex] ?? "").trim() !== "";
			let doc = docMarker(first);
			if (attached && ext === ".go") {
				doc ||=
					goGroups[nextIndex] !== null
						? goSpecExported(next)
						: goDocAt(lines, nextIndex, next);
			}
			if (attached && ext === ".proto")
				doc ||= protoDocAt(next, proto[nextIndex] ?? []);
			return { start: start + 1, lines: count, first, doc: generated || doc };
		}),
		ambiguous: false,
	};
}

export function isCgo(text: string): boolean {
	const go = LANG_BY_EXT[".go"];
	if (go === undefined) return false;
	const residues = scanComplete(go, text).residues;
	if (residues.some((line) => /^\s*import\s+"C"\s*$/.test(line))) return true;
	let inGroup = false;
	for (const line of residues) {
		if (!inGroup && /^\s*import\s*\(\s*$/.test(line)) {
			inGroup = true;
			continue;
		}
		if (inGroup && /^\s*"C"\s*$/.test(line)) return true;
		if (inGroup && /^\s*\)\s*$/.test(line)) inGroup = false;
	}
	return false;
}
