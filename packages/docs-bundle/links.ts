import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import {
	dirname,
	isAbsolute,
	join,
	posix,
	relative,
	resolve,
	sep,
} from "node:path";
import type { BrokenLink, BundlePage } from "./types.ts";

interface LinkTarget {
	start: number;
	end: number;
	raw: string;
}

function maskedMarkdown(source: string): string {
	const lines = source.split("\n");
	let fence: { marker: string; length: number } | undefined;
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] ?? "";
		if (fence !== undefined) {
			lines[index] = " ".repeat(line.length);
			const close = new RegExp(
				`^ {0,3}${fence.marker}{${fence.length},}[ \\t]*$`,
			);
			if (close.test(line)) fence = undefined;
			continue;
		}
		const open = /^ {0,3}(`{3,}|~{3,})/.exec(line);
		if (open !== null) {
			const marker = open[1] ?? "";
			fence = { marker: marker[0] ?? "`", length: marker.length };
			lines[index] = " ".repeat(line.length);
			continue;
		}
		lines[index] = line.replace(/(`+).*?\1/g, (code) =>
			" ".repeat(code.length),
		);
	}
	return lines.join("\n");
}

function linkTargets(source: string): LinkTarget[] {
	const masked = maskedMarkdown(source);
	const links: LinkTarget[] = [];
	const patterns = [
		/!?(?:\[[^\]]*\])\(\s*(<[^>]*>|[^\s)]+)(?:\s+[^)]*)?\)/dg,
		/^ {0,3}\[(?!\^)[^\]\n]+\]:[ \t]*(<[^>\n]*>|(?!["'])[^\s<>]+)(?:[ \t]+[^\n]*)?$/dgm,
	];
	for (const match of patterns.flatMap((pattern) => [
		...masked.matchAll(pattern),
	])) {
		const captured = match[1];
		const offset = match.indices?.[1]?.[0];
		if (
			captured === undefined ||
			match.index === undefined ||
			offset === undefined
		)
			continue;
		const start = offset;
		const raw = source.slice(start, start + captured.length);
		links.push({ start, end: start + captured.length, raw });
	}
	return links.sort((a, b) => a.start - b.start);
}

function unwrapTarget(target: string): string {
	return target.startsWith("<") && target.endsWith(">")
		? target.slice(1, -1)
		: target;
}

function targetParts(target: string): { path: string; suffix: string } {
	const index = target.search(/[?#]/);
	return index < 0
		? { path: target, suffix: "" }
		: { path: target.slice(0, index), suffix: target.slice(index) };
}

function routeKey(repo: string, path: string): string {
	return `${repo}:${path.replaceAll("\\", "/").replace(/^\.\//, "")}`;
}

function decodePath(path: string): string {
	try {
		return decodeURIComponent(path);
	} catch {
		return path;
	}
}

function parseGitHubBlob(
	url: string,
): { repo: string; path: string; suffix: string } | undefined {
	const match =
		/^https:\/\/github\.com\/([^/]+\/[^/]+)\/blob\/main\/(.+)$/i.exec(url);
	if (match === null) return undefined;
	const repo = match[1];
	const remainder = match[2];
	if (repo === undefined || remainder === undefined) return undefined;
	const { path, suffix } = targetParts(remainder);
	return { repo, path: decodePath(path), suffix };
}

function blobUrl(repo: string, path: string, suffix: string): string {
	return `https://github.com/${repo}/blob/main/${path.split("/").map(encodeURIComponent).join("/")}${suffix}`;
}

function withinRoot(root: string, path: string): boolean {
	const rel = relative(root, path);
	return (
		rel.length === 0 ||
		(!rel.startsWith(`..${sep}`) && rel !== ".." && !isAbsolute(rel))
	);
}

function repoTarget(root: string, page: BundlePage, target: string): string {
	const { path } = targetParts(target);
	const decodedPath = decodePath(path);
	const targetPath = decodedPath.startsWith("/")
		? decodedPath.slice(1)
		: decodedPath;
	const base = decodedPath.startsWith("/")
		? root
		: dirname(join(root, page.sourcePath));
	return posix.normalize(posix.relative(root, resolve(base, targetPath)));
}

export async function findBrokenLinks(
	root: string,
	repo: string,
	pages: readonly BundlePage[],
): Promise<BrokenLink[]> {
	const routes = new Map<string, string>();
	for (const page of pages)
		routes.set(routeKey(page.repo, page.sourcePath), page.route);
	const rootPath = resolve(root);
	const broken: BrokenLink[] = [];
	for (const page of pages) {
		if (page.repo !== repo) continue;
		const source = await readFile(join(rootPath, page.sourcePath), "utf8");
		for (const link of linkTargets(source)) {
			const target = unwrapTarget(link.raw);
			if (/^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(target)) continue;
			const { path } = targetParts(target);
			if (path.length === 0) continue;
			const resolvedTarget = repoTarget(rootPath, page, target);
			if (routes.has(routeKey(repo, resolvedTarget))) continue;
			const absoluteTarget = resolve(rootPath, resolvedTarget);
			if (
				!withinRoot(rootPath, absoluteTarget) ||
				!existsSync(absoluteTarget)
			) {
				broken.push({
					repo,
					sourcePath: page.sourcePath,
					target: resolvedTarget,
				});
			}
		}
	}
	return broken;
}

export function rewriteLinks(
	source: string,
	page: BundlePage,
	routes: ReadonlyMap<string, string>,
	root: string,
	onMissing?: (link: BrokenLink) => void,
): string {
	const rootPath = resolve(root);
	const links = linkTargets(source);
	if (links.length === 0) return source;
	let rewritten = "";
	let cursor = 0;
	for (const link of links) {
		rewritten += source.slice(cursor, link.start);
		const target = unwrapTarget(link.raw);
		const githubTarget = parseGitHubBlob(target);
		let output = target;
		if (githubTarget !== undefined) {
			const route = routes.get(routeKey(githubTarget.repo, githubTarget.path));
			if (route !== undefined) output = `${route}${githubTarget.suffix}`;
		} else if (!/^(?:[a-z][a-z\d+.-]*:|\/\/|#)/i.test(target)) {
			const { path, suffix } = targetParts(target);
			const resolvedTarget = repoTarget(rootPath, page, target);
			const route = routes.get(routeKey(page.repo, resolvedTarget));
			if (route !== undefined) {
				output = `${route}${suffix}`;
			} else {
				const absoluteTarget = resolve(rootPath, resolvedTarget);
				if (
					path.length > 0 &&
					(!withinRoot(rootPath, absoluteTarget) || !existsSync(absoluteTarget))
				) {
					onMissing?.({
						repo: page.repo,
						sourcePath: page.sourcePath,
						target: resolvedTarget,
					});
				}
				output = blobUrl(page.repo, resolvedTarget, suffix);
			}
		}
		rewritten +=
			output === target
				? link.raw
				: output.startsWith("https://") || !link.raw.startsWith("<")
					? output
					: `<${output}>`;
		cursor = link.end;
	}
	return rewritten + source.slice(cursor);
}
