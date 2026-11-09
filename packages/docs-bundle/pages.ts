import { readFile, readdir } from "node:fs/promises";
import { join, posix } from "node:path";
import { matchesAny } from "./config.ts";
import { isGatherable } from "./membership.ts";
import { routeSlug } from "./markdown.ts";
import type { BundlePage, FetchedSource, PublishConfig, SectionRule } from "./types.ts";

function sectionMatches(path: string, section: SectionRule): boolean {
	if (section.files?.includes(path)) return true;
	return section.prefix !== undefined && path.startsWith(section.prefix);
}

function packageName(path: string): string | undefined {
	const match = /^packages\/([^/]+)\//.exec(path);
	return match?.[1];
}

async function markdownFiles(root: string): Promise<string[]> {
	const files: string[] = [];
	async function walk(directory: string): Promise<void> {
		for (const entry of await readdir(join(root, directory), { withFileTypes: true })) {
			const path = directory.length === 0 ? entry.name : `${directory}/${entry.name}`;
			if (entry.isDirectory()) await walk(path);
			else if (entry.isFile() && isGatherable(path)) files.push(path);
		}
	}
	await walk("");
	return files.sort();
}

function isReadme(path: string): boolean {
	return /^packages\/[^/]+\/(?:[^/]+\/){0,}README\.md$/i.test(path);
}

export async function collectPages(source: FetchedSource, config: PublishConfig): Promise<BundlePage[]> {
	const files = await markdownFiles(source.root);
	const eligible = files.filter((path) => !matchesAny(path, config.exclude));
	const packageRules = config.packages;
	if (packageRules !== undefined) {
		for (const path of eligible) {
			const name = packageName(path);
			if (name === undefined) continue;
			const isAllowed = packageRules.allow.some((allowed) => name === allowed || path === allowed);
			if (isAllowed && isReadme(path)) {
				const depth = path.slice(`packages/${name}/`.length).split("/").length - 1;
				if (depth <= packageRules.readmeDepth) continue;
			}
			if (isAllowed && packageRules.allow.includes(path)) continue;
			throw new Error(`stray package page: ${path}`);
		}
	}

	const pages: BundlePage[] = [];
	for (const path of eligible) {
		const section = config.sections.find((rule) => sectionMatches(path, rule));
		if (config.sections.length > 0 && section === undefined) {
			if (source.config === "required") throw new Error(`unassigned page: ${path}`);
			continue;
		}
		if (section === undefined && source.config === "required" && packageName(path) !== undefined) {
			throw new Error(`stray package page: ${path}`);
		}
		const packageRoute = packageRules !== undefined && packageName(path) !== undefined && (packageRules.allow.includes(packageName(path) ?? "") || packageRules.allow.includes(path));
		const contentPath = source.mount.length === 0 ? path : posix.join(source.mount, path);
		let route: string;
		if (section !== undefined) {
			const prefix = section.prefix ?? "";
			const relativePath = section.files?.includes(path) ? path.split("/").at(-1) ?? path : path.slice(prefix.length);
			route = posix.join(section.route, routeSlug(relativePath));
		} else if (packageRoute && packageRules !== undefined) {
			const name = packageName(path) ?? "";
			const relativePath = path.slice(`packages/${name}/`.length).replace(/^README\.md$/i, "index.md");
			route = posix.join(packageRules.route, name, routeSlug(relativePath));
		} else {
			route = `/${source.repo}/${routeSlug(contentPath)}`;
		}
		pages.push({
			repo: source.repo,
			sourcePath: path,
			route,
			section: section?.id ?? null,
			pagefind: section?.pagefind ?? true,
		});
	}
	return pages;
}

export function assertNoRouteCollisions(pages: readonly BundlePage[]): void {
	const byRoute = new Map<string, BundlePage[]>();
	for (const page of pages) {
		const routePages = byRoute.get(page.route) ?? [];
		routePages.push(page);
		byRoute.set(page.route, routePages);
	}
	const collisions = [...byRoute].filter(([, routePages]) => routePages.length > 1);
	if (collisions.length === 0) return;
	const pageCount = collisions.reduce((count, [, routePages]) => count + routePages.length, 0);
	const routes = collisions.map(([route]) => route).join(", ");
	throw new Error(`${pageCount} page(s) collide on route(s): ${routes}`);
}

