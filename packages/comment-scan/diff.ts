import { scanAstro } from "./astro.ts";
import { scanJsx } from "./jsx.ts";
import { LANG_BY_EXT, LANG_BY_ID } from "./languages.ts";
import { type LanguageId, scanComplete } from "./scan.ts";

export type Residues = { residues: string[]; ambiguous: boolean };
export type { LanguageId } from "./scan.ts";

const PARSER_BY_EXT: Readonly<Record<string, (text: string) => Residues>> = {
	".tsx": scanJsx,
	".jsx": scanJsx,
	".astro": scanAstro,
};

const PARSER_BY_ID: Readonly<
	Partial<Record<LanguageId, (text: string) => Residues>>
> = {
	tsx: scanJsx,
	astro: scanAstro,
};

export type DiffLine = {
	kind: " " | "+" | "-";
	text: string;
	oldNo: number;
	newNo: number;
};

export type FileDiff = {
	path: string;
	oldPath: string;
	newPath: string;
	hunks: { lines: DiffLine[] }[];
	added: boolean;
	deleted: boolean;
	binary: boolean;
	metadataOnly: boolean;
};

function unquotePath(raw: string): string {
	if (!raw.startsWith('"')) return raw;
	return raw
		.slice(1, -1)
		.replace(/\\([\\"])/g, "$1")
		.replace(/\\t/g, "\t");
}

function stripPrefix(path: string): string {
	if (path === "/dev/null") return path;
	if (path.startsWith("a/") || path.startsWith("b/")) return path.slice(2);
	return path;
}

export function extOf(path: string): string {
	const base = path.slice(path.lastIndexOf("/") + 1);
	const dot = base.lastIndexOf(".");
	return dot <= 0 ? "" : base.slice(dot).toLowerCase();
}

const HUNK_RE = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

function parseHeaderPath(line: string): string {
	const rest = line.slice("diff --git ".length).trim();
	const at = rest.lastIndexOf(" b/");
	if (at === -1) return "";
	return stripPrefix(unquotePath(rest.slice(at + 1)));
}

export function parseDiff(diff: string): FileDiff[] {
	const files: FileDiff[] = [];
	const lines = diff.split("\n");
	let file: FileDiff | null = null;
	let oldPath = "",
		newPath = "",
		headerPath = "";
	let hunk: { lines: DiffLine[] } | null = null;
	let beforeFirstHunk = true;
	let renameFrom = false,
		renameTo = false,
		copyFrom = false,
		copyTo = false;
	let oldMode = false,
		newMode = false;
	let oldNo = 0,
		newNo = 0;
	const finishFile = () => {
		if (!file) return;
		file.added = oldPath === "/dev/null";
		file.deleted = newPath === "/dev/null";
		file.oldPath =
			oldPath && oldPath !== "/dev/null" ? stripPrefix(oldPath) : headerPath;
		file.newPath =
			newPath && newPath !== "/dev/null" ? stripPrefix(newPath) : headerPath;
		file.path = file.newPath || file.oldPath || headerPath;
		file.metadataOnly =
			(renameFrom && renameTo) || (copyFrom && copyTo) || (oldMode && newMode);
		files.push(file);
	};
	for (const line of lines) {
		if (line.startsWith("diff --git ")) {
			finishFile();
			file = {
				path: "",
				oldPath: "",
				newPath: "",
				hunks: [],
				added: false,
				deleted: false,
				binary: false,
				metadataOnly: false,
			};
			hunk = null;
			beforeFirstHunk = true;
			renameFrom = false;
			renameTo = false;
			copyFrom = false;
			copyTo = false;
			oldMode = false;
			newMode = false;
			oldPath = "";
			newPath = "";
			headerPath = parseHeaderPath(line);
			continue;
		}
		if (!file) continue;
		const match = HUNK_RE.exec(line);
		if (match) {
			oldNo = Number(match[1]);
			newNo = Number(match[2]);
			hunk = { lines: [] };
			file.hunks.push(hunk);
			beforeFirstHunk = false;
			continue;
		}
		if (beforeFirstHunk) {
			if (line.startsWith("rename from ")) renameFrom = true;
			if (line.startsWith("rename to ")) {
				renameTo = true;
				headerPath = stripPrefix(unquotePath(line.slice(10).trim()));
			}
			if (line.startsWith("copy from ")) copyFrom = true;
			if (line.startsWith("copy to ")) {
				copyTo = true;
				headerPath = stripPrefix(unquotePath(line.slice(9).trim()));
			}
			if (line.startsWith("old mode ")) oldMode = true;
			if (line.startsWith("new mode ")) newMode = true;
			if (line.startsWith("Binary files ") || line.startsWith("GIT binary "))
				file.binary = true;
			if (line.startsWith("--- ")) {
				oldPath = unquotePath(line.slice(4).trim());
				continue;
			}
			if (line.startsWith("+++ ")) {
				newPath = unquotePath(line.slice(4).trim());
				continue;
			}
			continue;
		}
		if (!hunk || line.startsWith("\\")) continue;
		const marker = line[0];
		if (marker === " ") {
			hunk.lines.push({ kind: " ", text: line.slice(1), oldNo, newNo });
			oldNo++;
			newNo++;
		} else if (marker === "+") {
			hunk.lines.push({ kind: "+", text: line.slice(1), oldNo, newNo });
			newNo++;
		} else if (marker === "-") {
			hunk.lines.push({ kind: "-", text: line.slice(1), oldNo, newNo });
			oldNo++;
		} else hunk = null;
	}
	finishFile();
	return files;
}

export function scanText(langId: LanguageId, text: string): Residues {
	text = text.replace(/\r\n/g, "\n");
	const parser = PARSER_BY_ID[langId];
	if (parser) return parser(text);
	const lang = LANG_BY_ID[langId];
	if (!lang) throw new Error(`no scanner for language ${langId}`);
	const scan = scanComplete(lang, text);
	return {
		residues: scan.residues,
		ambiguous: scan.underflow || scan.unclosed || scan.malformed,
	};
}

export function residueScannerFor(
	ext: string,
): ((text: string) => Residues) | null {
	const parser = PARSER_BY_EXT[ext];
	if (parser) return parser;
	const lang = LANG_BY_EXT[ext];
	if (!lang) return null;
	return (text) => {
		const normalized = text.replace(/\r\n/g, "\n");
		const scan = scanComplete(lang, normalized);
		return {
			residues: scan.residues,
			ambiguous: scan.underflow || scan.unclosed || scan.malformed,
		};
	};
}
