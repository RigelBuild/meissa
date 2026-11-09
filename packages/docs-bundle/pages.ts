import { readdir } from "node:fs/promises";
import { join, posix } from "node:path";
import { matchesAny } from "./config.ts";
import { routeSlug } from "./markdown.ts";
import { isGatherable } from "./membership.ts";
import type {
	BundlePage,
	FetchedSource,
	PublishConfig,
	SectionRule,
} from "./types.ts";

function sectionMatches(path: string, section: SectionRule): boolean {
	if (section.files?.includes(path)) return true;
	const prefix = section.prefix;
	return (
		prefix !== undefined &&
		(prefix.length === 0 ||
			path === prefix ||
			path.startsWith(prefix.endsWith("/") ? prefix : `${prefix}/`))
	);
}

async function markdownFiles(root: string): Promise<string[]> {
	const files: string[] = [];
	async function walk(directory: string): Promise<void> {
		for (const entry of await readdir(join(root, directory), {
			withFileTypes: true,
		})) {
			const path =
				directory.length === 0 ? entry.name : `${directory}/${entry.name}`;
			if (entry.isDirectory()) await walk(path);
			else if (entry.isFile() && isGatherable(path)) files.push(path);
		}
	}
	await walk("");
	return files.sort();
}

export async function collectPages(
	source: FetchedSource,
	config: PublishConfig,
): Promise<BundlePage[]> {
	const files = await markdownFiles(source.root);
	const eligible = files.filter((path) => !matchesAny(path, config.exclude));
	const packageRules = config.packages;
	if (packageRules !== undefined) {
		for (const path of eligible) {
			if (config.sections.some((section) => sectionMatches(path, section)))
				continue;
			const parts = path.split("/");
			const name = parts.at(-1);
			const readme =
				(name === "README.md" || name === "AGENTS.md") &&
				parts.length <= packageRules.readmeDepth;
			const allowed = packageRules.allow.some((entry) =>
				entry.endsWith("/") ? path.startsWith(entry) : path === entry,
			);
			if (!readme && !allowed) throw new Error(`stray package page: ${path}`);
		}
	}

	const pages: BundlePage[] = [];
	for (const path of eligible) {
		const section = config.sections.find((rule) => sectionMatches(path, rule));
		if (
			config.sections.length > 0 &&
			section === undefined &&
			packageRules === undefined
		) {
			if (source.config === "required")
				throw new Error(`unassigned page: ${path}`);
			continue;
		}
		const contentPath =
			source.mount.length === 0 ? path : posix.join(source.mount, path);
		let route: string;
		if (section !== undefined) {
			const prefix = section.prefix ?? "";
			const relativePath = section.files?.includes(path)
				? path.replace(/^\./, "").replaceAll("/", "-")
				: path.slice(prefix.length).replace(/^\//, "");
			route = `${section.route.replace(/\/$/, "")}${routeSlug(relativePath)}`;
		} else if (packageRules !== undefined) {
			const parts = path.split("/");
			const first = parts[0] ?? "";
			const nested = parts.length > 2 && packageRules.nested?.includes(first);
			const id = nested ? `${first}-${parts[1]}` : first;
			const rest = parts.slice(nested ? 2 : 1).join("/");
			route = routeSlug(
				`${packageRules.route.replace(/^\/+|\/+$/g, "")}/${id}${rest ? `/${rest}` : ""}`,
			);
		} else {
			route = `/${source.repo}${routeSlug(contentPath)}`;
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
	const collisions = [...byRoute].filter(
		([, routePages]) => routePages.length > 1,
	);
	if (collisions.length === 0) return;
	const pageCount = collisions.reduce(
		(count, [, routePages]) => count + routePages.length,
		0,
	);
	const routes = collisions.map(([route]) => route).join(", ");
	throw new Error(`${pageCount} page(s) collide on route(s): ${routes}`);
}
