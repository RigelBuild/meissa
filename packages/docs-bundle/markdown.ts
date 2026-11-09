import GithubSlugger from "github-slugger";

const FENCE_OPEN = /^ {0,3}(`{3,}|~{3,})(.*)$/;

function headingText(value: string): string {
	return value
		.replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
		.replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
		.replace(/\[([^\]]+)\]\[[^\]]*\]/g, "$1")
		.replace(/`+([^`]+)`+/g, "$1")
		.replace(/<[^>]+>/g, "")
		.replace(/\\([\\`*_{}[\]()#+.!<>|~-])/g, "$1")
		.replace(/[*_~]/g, "")
		.replace(/\s+/g, " ")
		.trim();
}

function firstH1Line(source: string): number {
	let fence: { marker: string; length: number } | undefined;
	const lines = source.split("\n");
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] ?? "";
		if (fence !== undefined) {
			const close = new RegExp(`^ {0,3}${fence.marker}{${fence.length},}[ \\t]*$`);
			if (close.test(line)) fence = undefined;
			continue;
		}
		const open = FENCE_OPEN.exec(line);
		if (open !== null) {
			const marker = open[1] ?? "";
			fence = { marker: marker[0] ?? "`", length: marker.length };
			continue;
		}
		if (/^ {0,3}#[ \t]+\S/.test(line)) return index;
	}
	return -1;
}

export function routeSlug(contentPath: string): string {
	const segments = contentPath.replaceAll("\\", "/").split("/").filter(Boolean);
	const last = segments.length - 1;
	return segments
		.map((segment, index) => {
			const value = index === last ? segment.replace(/\.md$/i, "") : segment;
			return new GithubSlugger().slug(value);
		})
		.join("/");
}

export function extractTitle(source: string, sourcePath: string): string {
	const index = firstH1Line(source);
	if (index >= 0) {
		const line = source.split("\n")[index] ?? "";
		const match = /^ {0,3}# +(.+?) *#* *$/.exec(line);
		if (match?.[1] !== undefined) return headingText(match[1]);
	}
	const filename = sourcePath.replaceAll("\\", "/").split("/").at(-1) ?? "";
	const stem = filename.replace(/\.md$/i, "").replace(/[-_]+/g, " ").trim();
	return stem.length === 0 ? "Untitled" : stem[0]!.toUpperCase() + stem.slice(1);
}

export function stripFirstH1(source: string): string {
	const index = firstH1Line(source);
	if (index < 0) return source;
	const lines = source.split("\n");
	lines.splice(index, 1);
	return lines.join("\n");
}
