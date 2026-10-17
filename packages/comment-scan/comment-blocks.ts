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

function exportedName(name: string): boolean {
	return /^\p{Lu}/u.test(name);
}

type GoToken = {
	kind: "identifier" | "string" | "symbol";
	value: string;
	line: number;
};

type GoGroups = {
	specByLine: Map<number, boolean>;
	groupByLine: Map<number, boolean>;
};

const GO_IDENTIFIER = /[\p{L}_][\p{L}\p{N}_]*/uy;

function goTokens(residues: readonly string[]): GoToken[] {
	const source = residues.join("\n");
	const tokens: GoToken[] = [];
	let line = 0;
	let lineStart = 0;
	let i = 0;
	let inRawString = false;
	while (i < source.length) {
		if (i === lineStart && !inRawString) {
			const newline = source.indexOf("\n", lineStart);
			const end = newline === -1 ? source.length : newline;
			let first = lineStart;
			while (first < end && /\s/u.test(source[first] ?? "")) first++;
			if (source.startsWith("//", first)) {
				i = end;
				continue;
			}
		}
		const char = source[i] ?? "";
		if (char === "\n") {
			if (!inRawString) tokens.push({ kind: "symbol", value: ";", line });
			line++;
			i++;
			lineStart = i;
			continue;
		}
		if (inRawString) {
			if (char === "`") inRawString = false;
			i++;
			continue;
		}
		if (/\s/u.test(char)) {
			i++;
			continue;
		}
		if (char === "`") {
			inRawString = true;
			i++;
			continue;
		}
		if (char === '"' || char === "'") {
			const start = i++;
			const quote = char;
			let escaped = false;
			while (i < source.length && source[i] !== "\n") {
				const current = source[i] ?? "";
				i++;
				if (escaped) escaped = false;
				else if (current === "\\") escaped = true;
				else if (current === quote) break;
			}
			tokens.push({ kind: "string", value: source.slice(start, i), line });
			continue;
		}
		GO_IDENTIFIER.lastIndex = i;
		const identifier = GO_IDENTIFIER.exec(source);
		if (identifier !== null) {
			tokens.push({ kind: "identifier", value: identifier[0], line });
			i = GO_IDENTIFIER.lastIndex;
			continue;
		}
		if ("(){};,".includes(char))
			tokens.push({ kind: "symbol", value: char, line });
		i++;
	}
	return tokens;
}

function namesExported(tokens: readonly GoToken[]): boolean {
	if (tokens[0]?.kind !== "identifier") return false;
	if (exportedName(tokens[0].value)) return true;
	for (let i = 1; tokens[i]?.value === ","; i += 2) {
		const name = tokens[i + 1];
		if (name?.kind !== "identifier") return false;
		if (exportedName(name.value)) return true;
	}
	return false;
}

function goGroups(tokens: readonly GoToken[]): GoGroups {
	const specByLine = new Map<number, boolean>();
	const groupByLine = new Map<number, boolean>();
	for (let i = 0; i < tokens.length; i++) {
		const keyword = tokens[i];
		const open = tokens[i + 1];
		if (
			keyword?.kind !== "identifier" ||
			(keyword.value !== "const" &&
				keyword.value !== "type" &&
				keyword.value !== "var") ||
			open?.value !== "("
		)
			continue;
		let parenDepth = 1;
		let braceDepth = 0;
		let segment: GoToken[] = [];
		let groupExported = false;
		let close = i + 1;
		const finishSpec = (): void => {
			const first = segment[0];
			if (first?.kind === "identifier") {
				const exported = namesExported(segment);
				specByLine.set(
					first.line,
					(specByLine.get(first.line) ?? false) || exported,
				);
				groupExported ||= exported;
			}
			segment = [];
		};
		for (let j = i + 2; j < tokens.length; j++) {
			const token = tokens[j];
			if (token === undefined) break;
			if (token.value === ")" && parenDepth === 1 && braceDepth === 0) {
				finishSpec();
				close = j;
				break;
			}
			if (token.value === ";" && parenDepth === 1 && braceDepth === 0) {
				finishSpec();
				continue;
			}
			segment.push(token);
			if (token.value === "(") parenDepth++;
			if (token.value === ")") parenDepth--;
			if (token.value === "{") braceDepth++;
			if (token.value === "}") braceDepth--;
		}
		groupByLine.set(keyword.line, groupExported);
		i = close;
	}
	return { specByLine, groupByLine };
}

function goSpecExported(line: string): boolean {
	return namesExported(goTokens([line]));
}

function goDocAt(residues: readonly string[], lineNo: number): boolean {
	const text = (residues[lineNo] ?? "").trim();
	if (/^package\s+[\p{L}_][\p{L}\p{N}_]*\s*$/u.test(text)) return true;
	const fn = /^func\s+(?:\([^)]*\)\s*)?([\p{L}_][\p{L}\p{N}_]*)/u.exec(
		text,
	)?.[1];
	if (fn !== undefined) return exportedName(fn);
	const declaration = /^(?:const|type|var)\s+(.+)$/.exec(text)?.[1];
	return declaration !== undefined && goSpecExported(declaration);
}

function protoScopes(residues: readonly string[]): ProtoScope[][] {
	const scopes: ProtoScope[][] = [];
	const stack: ProtoScope[] = [];
	let pending: ProtoScope | null = null;
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
				const kind = declaration ?? pending;
				stack.push(
					kind === "message" || kind === "service" || kind === "enum"
						? kind
						: "other",
				);
				pending = null;
			}
		}
		const declaration = /^\s*(message|service|enum)\s+[\w.]+\s*$/.exec(
			residue,
		)?.[1];
		if (
			declaration === "message" ||
			declaration === "service" ||
			declaration === "enum"
		)
			pending = declaration;
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
	const goGroupInfo = ext === ".go" ? goGroups(goTokens(scan.residues)) : null;
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
				const group = goGroupInfo?.groupByLine.get(nextIndex);
				const spec = goGroupInfo?.specByLine.get(nextIndex);
				doc ||=
					group !== undefined
						? group
						: (spec ?? goDocAt(scan.residues, nextIndex));
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
	const tokens = goTokens(scanComplete(go, text).residues);
	for (let i = 0; i < tokens.length; i++) {
		if (tokens[i]?.value !== "import") continue;
		const next = tokens[i + 1];
		if (next?.kind === "string" && next.value === '"C"') return true;
		if (next?.value !== "(") continue;
		let depth = 1;
		for (let j = i + 2; j < tokens.length; j++) {
			const token = tokens[j];
			if (token?.value === "(") depth++;
			if (token?.value === ")") {
				depth--;
				if (depth === 0) break;
			}
			if (depth === 1 && token?.kind === "string" && token.value === '"C"')
				return true;
		}
	}
	return false;
}
