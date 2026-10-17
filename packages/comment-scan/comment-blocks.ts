import { type Residues, residueScannerFor } from "./diff.ts";
import { LANG_BY_EXT } from "./languages.ts";
import { scanComplete } from "./scan.ts";

export type CommentBlock = {
	start: number;
	lines: number;
	first: string;
	doc: boolean;
};

type ProtoScope = "message" | "service" | "enum" | "other";
type GoGroup = "const" | "type" | "var";

function exportedName(name: string): boolean {
	return /^\p{Lu}/u.test(name);
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

function withoutGoLiterals(residues: readonly string[]): string[] {
	const lang = LANG_BY_EXT[".go"];
	if (lang === undefined) return [...residues];
	let quote = "";
	return residues.map((line) => {
		let result = "";
		for (let i = 0; i < line.length; i++) {
			const char = line[i] ?? "";
			if (quote !== "") {
				result += " ";
				if (quote !== "`" && char === "\\") {
					if (i + 1 < line.length) {
						result += " ";
						i++;
					}
				} else if (char === quote) quote = "";
				continue;
			}
			if (lang.strings.some(({ open }) => open === char)) {
				quote = char;
				result += " ";
			} else result += char;
		}
		return result;
	});
}

function groupHasExportedSpec(
	residues: readonly string[],
	start: number,
): boolean {
	let parenDepth = 0;
	let braceDepth = 0;
	for (let i = start; i < residues.length; i++) {
		const code = residues[i] ?? "";
		for (const char of code) {
			if (char === "(") parenDepth++;
			if (char === ")") parenDepth--;
			if (char === "{") braceDepth++;
			if (char === "}") braceDepth--;
		}
		if (
			i > start &&
			parenDepth === 1 &&
			braceDepth === 0 &&
			goSpecExported(code)
		)
			return true;
		if (i > start && parenDepth === 0) return false;
	}
	return false;
}

function goDocAt(residues: readonly string[], lineNo: number): boolean {
	const text = (residues[lineNo] ?? "").trim();
	if (/^package\s+[\p{L}_][\p{L}\p{N}_]*\s*$/u.test(text)) return true;
	const fn = /^func\s+(?:\([^)]*\)\s*)?([\p{L}_][\p{L}\p{N}_]*)/u.exec(
		text,
	)?.[1];
	if (fn !== undefined) return exportedName(fn);
	if (/^(const|type|var)\s*\(/.test(text))
		return groupHasExportedSpec(residues, lineNo);
	const declaration = /^(?:const|type|var)\s+(.+)$/.exec(text)?.[1];
	return declaration !== undefined && goSpecExported(declaration);
}

function goGroupKinds(residues: readonly string[]): (GoGroup | null)[] {
	const groups: (GoGroup | null)[] = [];
	let active: GoGroup | null = null;
	let parenDepth = 0;
	let braceDepth = 0;
	for (const code of residues) {
		const opener: RegExpExecArray | null =
			active === null ? /^\s*(const|type|var)\s*\(/.exec(code) : null;
		if (opener !== null) {
			const kind: string | undefined = opener[1];
			if (kind === "const" || kind === "type" || kind === "var") {
				active = kind;
				parenDepth = 0;
				braceDepth = 0;
			}
		}
		groups.push(
			active !== null && opener === null && parenDepth === 1 && braceDepth === 0
				? active
				: null,
		);
		if (active === null) continue;
		for (const char of code) {
			if (char === "(") parenDepth++;
			if (char === ")") parenDepth--;
			if (char === "{") braceDepth++;
			if (char === "}") braceDepth--;
		}
		if (parenDepth === 0 && braceDepth === 0) active = null;
	}
	return groups;
}

function protoScopes(residues: readonly string[]): ProtoScope[][] {
	const scopes: ProtoScope[][] = [];
	const stack: ProtoScope[] = [];
	for (const residue of residues) {
		scopes.push([...stack]);
		let quote = "";
		let escaped = false;
		for (let i = 0; i < residue.length; i++) {
			const char = residue[i] ?? "";
			if (quote !== "") {
				if (escaped) escaped = false;
				else if (char === "\\") escaped = true;
				else if (char === quote) quote = "";
				continue;
			}
			if (char === '"' || char === "'") {
				quote = char;
				continue;
			}
			if (char === "}") stack.pop();
			if (char === "{") {
				const declaration = /\b(message|service|enum)\s+[\w.]+\s*$/.exec(
					residue.slice(0, i),
				)?.[1];
				stack.push(
					declaration === "message"
						? "message"
						: declaration === "service"
							? "service"
							: declaration === "enum"
								? "enum"
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
	if (scopes.includes("enum") && /^[\w]+\s*=\s*-?\d+\b/.test(text)) return true;
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

function generatedMarkerInComment(
	line: string,
	residue: string,
	directive?: RegExp,
): boolean {
	let residueIndex = 0;
	let commentText = "";
	for (const char of line) {
		if (residueIndex < residue.length && char === residue[residueIndex]) {
			residueIndex++;
		} else {
			commentText += char;
		}
	}
	const marker = /Code generated\b.*DO NOT EDIT\.|@generated\b/;
	return (
		marker.test(commentText) ||
		(directive?.test(line.trimStart()) === true && marker.test(line))
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
	text = text.replace(/\r\n/g, "\n");
	const scanner = residueScannerFor(ext);
	if (scanner === null) return null;
	if (ext === ".go" && isCgo(text)) return { blocks: [], ambiguous: true };
	const scan: Residues = scanner(text);

	if (scan.ambiguous) return { blocks: [], ambiguous: true };
	const lines = text.split("\n");
	const generated = lines
		.slice(0, 5)
		.some((line, index) =>
			generatedMarkerInComment(
				line,
				scan.residues[index] ?? "",
				LANG_BY_EXT[ext]?.directive,
			),
		);
	const runs = commentRuns(lines, scan.residues);
	const goResidues =
		ext === ".go" ? withoutGoLiterals(scan.residues) : scan.residues;
	const goGroups = ext === ".go" ? goGroupKinds(goResidues) : [];
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
						? goSpecExported(goResidues[nextIndex] ?? "")
						: goDocAt(goResidues, nextIndex);
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
