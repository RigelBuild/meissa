import { type Residues, residueScannerFor } from "./diff.ts";
import { LANG_BY_EXT } from "./languages.ts";

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
	kind: "identifier" | "keyword" | "literal" | "string" | "symbol";
	value: string;
	line: number;
	braceDepth: number;
};

type GoAnalysis = {
	docByLine: Map<number, boolean>;
	specByLine: Map<number, boolean>;
};

const GO_KEYWORDS = new Set([
	"break",
	"case",
	"chan",
	"const",
	"continue",
	"default",
	"defer",
	"else",
	"fallthrough",
	"for",
	"func",
	"go",
	"goto",
	"if",
	"import",
	"interface",
	"map",
	"package",
	"range",
	"return",
	"select",
	"struct",
	"switch",
	"type",
	"var",
]);
const GO_SEMICOLON_KEYWORDS = new Set([
	"break",
	"continue",
	"fallthrough",
	"return",
]);

function canInsertSemicolon(token: GoToken | undefined): boolean {
	if (token === undefined) return false;
	if (
		token.kind === "identifier" ||
		token.kind === "string" ||
		token.kind === "literal"
	)
		return true;
	if (token.kind === "keyword") return GO_SEMICOLON_KEYWORDS.has(token.value);
	return [")", "]", "}", "++", "--"].includes(token.value);
}

const GO_IDENTIFIER = /[\p{L}_][\p{L}\p{N}_]*/uy;

function goTokens(source: string): GoToken[] {
	source = source.replace(/\r\n/g, "\n");
	const tokens: GoToken[] = [];
	let line = 0;
	let braceDepth = 0;
	let previous: GoToken | undefined;
	let i = 0;
	const push = (
		kind: GoToken["kind"],
		value: string,
		tokenLine = line,
	): void => {
		const depth = value === "}" ? Math.max(0, braceDepth - 1) : braceDepth;
		const token = { kind, value, line: tokenLine, braceDepth: depth };
		tokens.push(token);
		previous = token;
		if (value === "{") braceDepth++;
		if (value === "}") braceDepth = Math.max(0, braceDepth - 1);
	};
	while (i < source.length) {
		const char = source[i] ?? "";
		if (char === "\n") {
			if (canInsertSemicolon(previous)) push("symbol", ";");
			line++;
			i++;
			continue;
		}
		if (/\s/u.test(char)) {
			i++;
			continue;
		}
		if (char === "/" && source[i + 1] === "/") {
			const end = source.indexOf("\n", i);
			i = end === -1 ? source.length : end;
			continue;
		}
		if (char === "/" && source[i + 1] === "*") {
			i += 2;
			while (
				i < source.length &&
				!(source[i] === "*" && source[i + 1] === "/")
			) {
				if (source[i] === "\n") {
					if (canInsertSemicolon(previous)) push("symbol", ";");
					line++;
				}
				i++;
			}
			if (i < source.length) i += 2;
			continue;
		}
		if (char === "`") {
			const start = i++;
			const startLine = line;
			while (i < source.length && source[i] !== "`") {
				if (source[i] === "\n") line++;
				i++;
			}
			if (i < source.length) i++;
			push("string", source.slice(start, i), startLine);
			continue;
		}
		if (char === '"' || char === "'") {
			const start = i++;
			const startLine = line;
			const quote = char;
			let escaped = false;
			while (i < source.length && source[i] !== "\n") {
				const current = source[i] ?? "";
				i++;
				if (escaped) escaped = false;
				else if (current === "\\") escaped = true;
				else if (current === quote) break;
			}
			push(
				quote === '"' ? "string" : "literal",
				source.slice(start, i),
				startLine,
			);
			continue;
		}
		if (/[0-9]/u.test(char)) {
			const start = i;
			while (i < source.length && /[\p{L}\p{N}_.]/u.test(source[i] ?? "")) i++;
			push("literal", source.slice(start, i));
			continue;
		}
		GO_IDENTIFIER.lastIndex = i;
		const identifier = GO_IDENTIFIER.exec(source);
		if (identifier !== null) {
			const value = identifier[0];
			push(GO_KEYWORDS.has(value) ? "keyword" : "identifier", value);
			i = GO_IDENTIFIER.lastIndex;
			continue;
		}
		const pair = source.slice(i, i + 2);
		if (pair === "++" || pair === "--") {
			push("symbol", pair);
			i += 2;
			continue;
		}
		push("symbol", char);
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

function goAnalysis(tokens: readonly GoToken[]): GoAnalysis {
	const docByLine = new Map<number, boolean>();
	const specByLine = new Map<number, boolean>();
	const recordDeclaration = (line: number, doc: boolean): void => {
		if (!docByLine.has(line)) docByLine.set(line, doc);
	};
	for (let i = 0; i < tokens.length; i++) {
		const keyword = tokens[i];
		if (keyword?.kind !== "keyword") continue;
		if (keyword.value === "package") {
			if (keyword.braceDepth === 0 && tokens[i + 1]?.kind === "identifier")
				recordDeclaration(keyword.line, true);
			continue;
		}
		if (keyword.value === "func") {
			let nameIndex = i + 1;
			if (tokens[nameIndex]?.value === "(") {
				let depth = 1;
				nameIndex++;
				while (nameIndex < tokens.length && depth > 0) {
					const token = tokens[nameIndex];
					if (token?.value === "(") depth++;
					if (token?.value === ")") depth--;
					nameIndex++;
				}
			}
			const name = tokens[nameIndex];
			if (keyword.braceDepth === 0 && name?.kind === "identifier")
				recordDeclaration(keyword.line, exportedName(name.value));
			continue;
		}
		if (
			keyword.value !== "const" &&
			keyword.value !== "type" &&
			keyword.value !== "var"
		)
			continue;
		if (keyword.braceDepth !== 0) {
			recordDeclaration(keyword.line, false);
			continue;
		}
		const open = tokens[i + 1];
		if (open?.value !== "(") {
			const spec: GoToken[] = [];
			let parens = 0;
			let brackets = 0;
			let braces = 0;
			for (let j = i + 1; j < tokens.length; j++) {
				const token = tokens[j];
				if (token === undefined) break;
				if (
					token.value === ";" &&
					parens === 0 &&
					brackets === 0 &&
					braces === 0
				)
					break;
				spec.push(token);
				if (token.value === "(") parens++;
				if (token.value === ")") parens--;
				if (token.value === "[") brackets++;
				if (token.value === "]") brackets--;
				if (token.value === "{") braces++;
				if (token.value === "}") braces--;
			}
			recordDeclaration(keyword.line, namesExported(spec));
			continue;
		}
		let parenDepth = 1;
		let braceDepth = 0;
		let segment: GoToken[] = [];
		let groupExported = false;
		let close = i + 1;
		const finishSpec = (): void => {
			const first = segment[0];
			if (first?.kind === "identifier") {
				const exported = namesExported(segment);
				if (!specByLine.has(first.line)) specByLine.set(first.line, exported);
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
		if (keyword.braceDepth === 0)
			recordDeclaration(keyword.line, groupExported);
		else recordDeclaration(keyword.line, false);
		i = close;
	}
	return { docByLine, specByLine };
}

function goDocAt(analysis: GoAnalysis, lineNo: number): boolean {
	return (
		analysis.docByLine.get(lineNo) ?? analysis.specByLine.get(lineNo) ?? false
	);
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
// Per-line proto text with comments blanked and line count kept, so `message/* c */M {` stays two tokens.
function protoScopeText(text: string): string[] {
	let out = "";
	let quote = "";
	for (let i = 0; i < text.length; i++) {
		const char = text[i] ?? "";
		if (quote !== "") {
			out += char;
			if (char === "\\") out += text[++i] ?? "";
			else if (char === quote) quote = "";
			continue;
		}
		if (char === '"' || char === "'") quote = char;
		else if (char === "/" && text[i + 1] === "/") {
			while (i < text.length && text[i] !== "\n") i++;
			out += "\n";
			continue;
		} else if (char === "/" && text[i + 1] === "*") {
			const end = text.indexOf("*/", i + 2);
			const body = text.slice(i, end === -1 ? text.length : end + 2);
			out += ` ${"\n".repeat(body.split("\n").length - 1)}`;
			i += body.length - 1;
			continue;
		}
		out += char;
	}
	return out.split("\n");
}
function protoDocAt(line: string, scopes: readonly ProtoScope[]): boolean {
	const text = line.replace(/\/\*[\s\S]*?\*\//g, " ").trim();
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
	const goTokensResult = ext === ".go" ? goTokens(text) : [];
	if (ext === ".go" && isCgoTokens(goTokensResult))
		return { blocks: [], ambiguous: true };
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
	const goGroupInfo = ext === ".go" ? goAnalysis(goTokensResult) : null;
	const proto = ext === ".proto" ? protoScopes(protoScopeText(text)) : [];
	return {
		blocks: runs.map(({ start, lines: count }) => {
			const first = lines[start] ?? "";
			const nextIndex = start + count;
			const next = lines[nextIndex] ?? "";
			const attached =
				next.trim() !== "" && (scan.residues[nextIndex] ?? "").trim() !== "";
			let doc = docMarker(first);
			if (attached && ext === ".go" && goGroupInfo !== null) {
				doc ||= goDocAt(goGroupInfo, nextIndex);
			}
			if (attached && ext === ".proto")
				doc ||= protoDocAt(next, proto[nextIndex] ?? []);
			return { start: start + 1, lines: count, first, doc: generated || doc };
		}),
		ambiguous: false,
	};
}

export function isCgo(text: string): boolean {
	return isCgoTokens(goTokens(text));
}

function isCgoTokens(tokens: readonly GoToken[]): boolean {
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
