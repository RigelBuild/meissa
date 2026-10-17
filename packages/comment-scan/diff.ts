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

const C_ESCAPES: Record<string, number> = {
	a: 7,
	b: 8,
	f: 12,
	n: 10,
	r: 13,
	t: 9,
	v: 11,
};

// Git C-quotes unusual paths; octal escapes are UTF-8 bytes, so decode bytes, not chars.
function unquotePath(raw: string): string {
	if (raw.length < 2 || !raw.startsWith('"') || !raw.endsWith('"')) return raw;
	const encoder = new TextEncoder();
	const bytes: number[] = [];
	for (const part of raw
		.slice(1, -1)
		.split(/(\\[0-3][0-7]{2}|\\[abfnrtv"\\])/u)) {
		const code = part.startsWith("\\") ? part.slice(1) : "";
		if (/^[0-7]{3}$/u.test(code)) bytes.push(Number.parseInt(code, 8));
		else if (code) bytes.push(C_ESCAPES[code] ?? code.charCodeAt(0));
		else bytes.push(...encoder.encode(part));
	}
	return new TextDecoder().decode(new Uint8Array(bytes));
}

// Git ends a ---/+++ path that holds a space with one tab; the name's own spaces stay.
function diffLinePath(rest: string): string {
	return unquotePath(rest.endsWith("\t") ? rest.slice(0, -1) : rest);
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

// The b-side of `diff --git <a> <b>`. Without a rename both halves name one path,
// so an unquoted header splits at its midpoint even when the path holds " b/".
function parseHeaderPath(line: string): string {
	const rest = line.slice("diff --git ".length).replace(/\r$/u, "");
	if (rest.endsWith('"')) {
		const open = rest.lastIndexOf(' "');
		if (open !== -1) return stripPrefix(unquotePath(rest.slice(open + 1)));
	}
	const middle = (rest.length - 1) / 2;
	if (
		Number.isInteger(middle) &&
		rest[middle] === " " &&
		rest.slice(2, middle) === rest.slice(middle + 3)
	)
		return rest.slice(middle + 3);
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
		headerPath = "",
		fromPath = "";
	let hunk: { lines: DiffLine[] } | null = null;
	let beforeFirstHunk = true;
	let renameFrom = false,
		renameTo = false,
		copyFrom = false,
		copyTo = false;
	let oldMode = false,
		newMode = false,
		newFile = false,
		deletedFile = false;
	let oldNo = 0,
		newNo = 0;
	const finishFile = () => {
		if (!file) return;
		// Empty and binary files have no ---/+++ lines; their mode header says added or deleted.
		file.added = oldPath === "/dev/null" || newFile;
		file.deleted = newPath === "/dev/null" || deletedFile;
		file.oldPath =
			oldPath && oldPath !== "/dev/null"
				? stripPrefix(oldPath)
				: fromPath || headerPath;
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
			fromPath = "";
			newFile = false;
			deletedFile = false;
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
			// Header lines of a CRLF-saved diff end in \r; content lines keep theirs.
			const header = line.replace(/\r$/u, "");
			// Rename and copy records carry repository paths with no a/ or b/ prefix.
			if (header.startsWith("rename from ")) {
				renameFrom = true;
				fromPath = unquotePath(header.slice(12));
			}
			if (header.startsWith("rename to ")) {
				renameTo = true;
				headerPath = unquotePath(header.slice(10));
			}
			if (header.startsWith("copy from ")) {
				copyFrom = true;
				fromPath = unquotePath(header.slice(10));
			}
			if (header.startsWith("copy to ")) {
				copyTo = true;
				headerPath = unquotePath(header.slice(8));
			}
			if (header.startsWith("old mode ")) oldMode = true;
			if (header.startsWith("new mode ")) newMode = true;
			if (header.startsWith("new file mode ")) newFile = true;
			if (header.startsWith("deleted file mode ")) deletedFile = true;
			if (
				header.startsWith("Binary files ") ||
				header.startsWith("GIT binary ")
			)
				file.binary = true;
			if (header.startsWith("--- ")) {
				oldPath = diffLinePath(header.slice(4));
				continue;
			}
			if (header.startsWith("+++ ")) {
				newPath = diffLinePath(header.slice(4));
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
	if (parser) return (text) => parser(text.replace(/\r\n/g, "\n"));
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
