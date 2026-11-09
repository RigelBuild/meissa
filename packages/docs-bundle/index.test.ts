import { afterAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { isExcluded, parseExclusions } from "./exclusions.ts";
import {
	assertNoRouteCollisions,
	type BundlePage,
	collectPages,
	extractTitle,
	type FetchedSource,
	findBrokenLinks,
	isGatherable,
	readFetched,
	readPublishConfig,
	rewriteLinks,
	routeSlug,
	stripFirstH1,
	writeManifest,
} from "./index.ts";

const temporaryDirectories: string[] = [];

async function fixture(): Promise<string> {
	const root = await mkdtemp(join(tmpdir(), "docs-bundle-"));
	temporaryDirectories.push(root);
	return root;
}

const source: FetchedSource = {
	repo: "acme/widgets",
	mount: "",
	config: "default",
	root: "/tmp/acme-widgets",
	sha: "abc123",
};

const page = (sourcePath: string, route: string): BundlePage => ({
	repo: source.repo,
	sourcePath,
	route,
	section: null,
	pagefind: true,
});

afterAll(async () => {
	await Promise.all(
		temporaryDirectories.map((directory) =>
			rm(directory, { recursive: true, force: true }),
		),
	);
});

describe("docs bundle helpers", () => {
	test("parses Markdown exclusion config and applies glob exclusions", () => {
		const exclusions = parseExclusions(
			'{\n  // generated release files\n  "ignores": ["private/**"]\n}',
		);
		expect(exclusions).toEqual([
			"private/**",
			"**/outputs/**",
			"**/CHANGELOG.md",
		]);
		expect(isExcluded("private/notes.md", exclusions)).toBe(true);
		expect(isExcluded("public/guide.md", exclusions)).toBe(false);
	});
	test("uses GitHub slug rules for every route segment", () => {
		expect(routeSlug("Guides/Hello, World!.md")).toBe("guides/hello-world");
		expect(routeSlug("README.md")).toBe("readme");
	});

	test("extracts the first H1 outside code fences and strips only that heading", () => {
		const markdown = "```md\n# Example\n```\n\n# Real **title**\n\nBody\n";
		expect(extractTitle(markdown, "docs/page.md")).toBe("Real title");
		expect(stripFirstH1(markdown)).toBe("```md\n# Example\n```\n\n\nBody\n");
	});
	test("preserves a C# heading and strips only whitespace-delimited closing hashes", () => {
		expect(extractTitle("# C#\n", "docs/csharp.md")).toBe("C#");
		expect(extractTitle("# Title ##\n", "docs/title.md")).toBe("Title");
		expect(stripFirstH1("# C#\nBody\n")).toBe("Body\n");
	});
	test("rewrites the href when link text equals its target", () => {
		const current = page("index.md", "/guide");
		const routes = new Map([["acme/widgets:next.md", "/guide/next"]]);
		expect(
			rewriteLinks("[next.md](next.md)", current, routes, "/tmp/acme-widgets"),
		).toBe("[next.md](/guide/next)");
	});
	test("collects an allowed package README for a required source", async () => {
		const root = await fixture();
		await mkdir(join(root, "packages", "widgets"), { recursive: true });
		await writeFile(
			join(root, "packages", "widgets", "README.md"),
			"# Widgets\n",
		);
		const pages = await collectPages(
			{ ...source, root, config: "required" },
			{
				exclude: [],
				sections: [],
				packages: { route: "/packages", allow: ["widgets"], readmeDepth: 3 },
			},
		);
		expect(pages.map(({ sourcePath, route }) => [sourcePath, route])).toEqual([
			["packages/widgets/README.md", "/packages/widgets/index"],
		]);
	});
	test("does not match a section prefix that is a sibling name", async () => {
		const root = await fixture();
		await mkdir(join(root, "docs"), { recursive: true });
		await writeFile(join(root, "docs", "guidelines.md"), "# Guidelines\n");
		await expect(
			collectPages(
				{ ...source, root, config: "required" },
				{
					exclude: [],
					sections: [
						{
							id: "guide",
							label: "Guide",
							route: "/guide",
							prefix: "docs/guide",
						},
					],
				},
			),
		).rejects.toThrow("unassigned page: docs/guidelines.md");
	});

	test("recognizes only eligible Markdown paths", () => {
		for (const path of [
			"README.md",
			"docs/start.MD",
			".github/PULL_REQUEST_TEMPLATE.md",
		]) {
			expect(isGatherable(path)).toBe(true);
		}
		for (const path of [
			"CHANGELOG.md",
			"docs/designs/record.md",
			"node_modules/x.md",
			"dist/x.md",
			"outputs/x.md",
			".hidden/page.md",
			"src/code.ts",
		]) {
			expect(isGatherable(path)).toBe(false);
		}
	});

	test("uses defaults when config is absent and requires explicit config when required", async () => {
		const root = await fixture();
		await expect(readPublishConfig(root, "required")).rejects.toThrow(
			"docs-bundle.toml",
		);
		expect(await readPublishConfig(root, "default")).toEqual({
			exclude: [],
			sections: [],
		});
	});

	test("parses TOML sections and package allow rules", async () => {
		const root = await fixture();
		await writeFile(
			join(root, "docs-bundle.toml"),
			`exclude = ["private/**"]\n\n[[sections]]\nid = "guide"\nlabel = "Guide"\nroute = "/guide"\nprefix = "docs/guide/"\npagefind = false\n\n[packages]\nroute = "/packages"\nallow = ["widgets"]\nreadme_depth = 2\n`,
		);
		const config = await readPublishConfig(root, "required");
		expect(config.exclude).toEqual(["private/**"]);
		expect(config.sections[0]).toEqual({
			id: "guide",
			label: "Guide",
			route: "/guide",
			prefix: "docs/guide/",
			pagefind: false,
		});
		expect(config.packages).toEqual({
			route: "/packages",
			allow: ["widgets"],
			readmeDepth: 2,
		});
	});

	test("collects default pages beneath their repository route", async () => {
		const root = await fixture();
		await mkdir(join(root, "docs"), { recursive: true });
		await writeFile(join(root, "README.md"), "# Widgets\n");
		await writeFile(join(root, "docs", "guide.md"), "# Guide\n");
		const pages = await collectPages(
			{ ...source, root },
			{ exclude: [], sections: [] },
		);
		expect(pages.map(({ sourcePath, route }) => [sourcePath, route])).toEqual([
			["README.md", "/acme/widgets/readme"],
			["docs/guide.md", "/acme/widgets/docs/guide"],
		]);
	});

	test("throws when package docs exceed configured allow rules", async () => {
		const root = await fixture();
		await mkdir(join(root, "packages", "stray"), { recursive: true });
		await writeFile(join(root, "packages", "stray", "README.md"), "# Stray\n");
		await expect(
			collectPages(
				{ ...source, root },
				{
					exclude: [],
					sections: [],
					packages: { route: "/packages", allow: ["widgets"], readmeDepth: 3 },
				},
			),
		).rejects.toThrow("stray package page");
	});

	test("throws when two pages claim one route", () => {
		expect(() =>
			assertNoRouteCollisions([
				page("docs/a.md", "/guide"),
				page("docs/b.md", "/guide"),
			]),
		).toThrow("/guide");
	});

	test("rewrites bundled links, existing unbundled files, and missing files from the supplied root", async () => {
		const root = await fixture();
		expect(root).not.toBe(process.cwd());
		await mkdir(join(root, "docs"), { recursive: true });
		await writeFile(join(root, "docs", "asset.png"), "image");
		const current = page("docs/index.md", "/guide");
		const routes = new Map([["acme/widgets:docs/next.md", "/guide/next"]]);
		const missing: { repo: string; sourcePath: string; target: string }[] = [];
		const rewritten = rewriteLinks(
			"[next](next.md) [asset](asset.png) [missing](absent.md#part)",
			current,
			routes,
			root,
			(link) => missing.push(link),
		);
		expect(rewritten).toContain("](/guide/next)");
		expect(rewritten).toContain(
			"](https://github.com/acme/widgets/blob/main/docs/asset.png)",
		);
		expect(rewritten).toContain(
			"](https://github.com/acme/widgets/blob/main/docs/absent.md#part)",
		);
		expect(missing).toEqual([
			{
				repo: "acme/widgets",
				sourcePath: "docs/index.md",
				target: "docs/absent.md",
			},
		]);
	});

	test("rewrites bundled GitHub blob links to their site routes", () => {
		const current = page("docs/index.md", "/guide");
		const routes = new Map([["acme/widgets:docs/next.md", "/guide/next"]]);
		expect(
			rewriteLinks(
				"[next](https://github.com/acme/widgets/blob/main/docs/next.md#usage)",
				current,
				routes,
				"/tmp/checkout-not-cwd",
			),
		).toBe("[next](/guide/next#usage)");
	});
	test("finds broken relative links but permits files that are present", async () => {
		const root = await fixture();
		await mkdir(join(root, "docs"), { recursive: true });
		await writeFile(
			join(root, "docs", "index.md"),
			"[exists](asset.png) [missing](missing.md)\n",
		);
		await writeFile(join(root, "docs", "asset.png"), "image");
		expect(
			await findBrokenLinks(root, source.repo, [
				page("docs/index.md", "/docs"),
			]),
		).toEqual([
			{
				repo: source.repo,
				sourcePath: "docs/index.md",
				target: "docs/missing.md",
			},
		]);
	});

	test("reads absent fetched lists as empty and writes the build manifest", async () => {
		const root = await fixture();
		expect(await readFetched(join(root, "missing.json"))).toEqual([]);
		const manifest = {
			docsRepo: { repo: "RigelBuild/docs", sha: "def456" },
			sources: [{ repo: source.repo, ref: "main", sha: source.sha, pages: 2 }],
		};
		await writeManifest(join(root, "dist"), manifest);
		expect(await Bun.file(join(root, "dist", "_bundle.json")).json()).toEqual(
			manifest,
		);
	});
});
