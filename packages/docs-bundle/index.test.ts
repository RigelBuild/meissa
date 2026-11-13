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
	test("matches content routes including index and mdx", () => {
		expect(routeSlug("Guides/Index.mdx")).toBe("/guides");
		expect(routeSlug("README.md")).toBe("/readme");
	});
	test("keeps nested design directories outside the root design corpus", () => {
		expect(isGatherable("team/docs/designs/page.md")).toBe(true);
		expect(isGatherable("docs/designs/page.md")).toBe(false);
	});
	test("routes catch-all and explicit files without collisions", async () => {
		const root = await fixture();
		for (const path of [
			"README.md",
			"docs/README.md",
			"apps/widget/README.md",
			"tools/item/AGENTS.md",
			"notes/guide.md",
		]) {
			await mkdir(join(root, path, ".."), { recursive: true });
			await writeFile(join(root, path), "# Page");
		}
		const config = {
			exclude: [],
			sections: [
				{
					id: "contributing",
					label: "Contributing",
					route: "/contributing",
					files: ["README.md", "docs/README.md"],
				},
			],
			packages: {
				route: "/packages",
				allow: ["notes/"],
				readmeDepth: 3,
				nested: ["apps"],
			},
		};
		const pages = await collectPages(
			{ ...source, root, config: "required" },
			config,
		);
		expect(
			Object.fromEntries(pages.map((p) => [p.sourcePath, p.route])),
		).toEqual({
			"README.md": "/contributing/readme",
			"docs/README.md": "/contributing/docs-readme",
			"apps/widget/README.md": "/packages/apps-widget/readme",
			"tools/item/AGENTS.md": "/packages/tools/item/agents",
			"notes/guide.md": "/packages/notes/guide",
		});
		await writeFile(join(root, "tools/item/stray.md"), "# Stray");
		await expect(
			collectPages({ ...source, root, config: "required" }, config),
		).rejects.toThrow("stray package page: tools/item/stray.md");
	});
	test("does not treat a quoted statement after a reference as a definition", async () => {
		const root = await fixture();
		await writeFile(join(root, "index.md"), '[rule]: "A quoted statement."\n');
		expect(
			await findBrokenLinks(root, source.repo, [page("index.md", "/")]),
		).toEqual([]);
	});
	test("rewrites and checks reference targets containing apostrophes", async () => {
		const root = await fixture();
		const input = "[k]: docs/don't.md\n";
		const current = page("index.md", "/");
		await writeFile(join(root, "index.md"), input);
		expect(
			rewriteLinks(
				input,
				current,
				new Map([["acme/widgets:docs/don't.md", "/guide"]]),
				root,
			),
		).toBe("[k]: /guide\n");
		expect(await findBrokenLinks(root, source.repo, [current])).toEqual([
			{ repo: source.repo, sourcePath: "index.md", target: "docs/don't.md" },
		]);
	});
	test("rejects root readmes not claimed by a section", async () => {
		const root = await fixture();
		await writeFile(join(root, "README.md"), "# Root\n");
		await expect(
			collectPages(
				{ ...source, root, config: "required" },
				{
					exclude: [],
					sections: [],
					packages: { route: "/packages", allow: [], readmeDepth: 3 },
				},
			),
		).rejects.toThrow("stray package page: README.md");
	});
	test("uses GitHub slug rules for every route segment", () => {
		expect(routeSlug("Guides/Hello, World!.md")).toBe("/guides/hello-world");
		expect(routeSlug("README.md")).toBe("/readme");
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
	test("preserves angle-wrapped external links and fragments", () => {
		const input = "[x](<https://example.com/a b>) [x](<#sec>)";
		expect(
			rewriteLinks(
				input,
				page("index.md", "/"),
				new Map(),
				"/tmp/acme-widgets",
			),
		).toBe(input);
	});
	test("ignores footnote definitions when rewriting and checking links", async () => {
		const root = await fixture();
		const input = "[^1]: This is a footnote.\n";
		await writeFile(join(root, "index.md"), input);
		expect(rewriteLinks(input, page("index.md", "/"), new Map(), root)).toBe(
			input,
		);
		expect(
			await findBrokenLinks(root, source.repo, [page("index.md", "/")]),
		).toEqual([]);
	});
	test("handles bare percent signs in existing and missing paths", async () => {
		const root = await fixture();
		const current = page("index.md", "/");
		await writeFile(join(root, "100%.md"), "# Present");
		await writeFile(
			join(root, "index.md"),
			"[exists](100%.md) [missing](99%.md)",
		);
		const missing: string[] = [];
		expect(
			rewriteLinks(
				"[exists](100%.md) [missing](99%.md)",
				current,
				new Map(),
				root,
				(link) => missing.push(link.target),
			),
		).toBe(
			"[exists](https://github.com/acme/widgets/blob/main/100%25.md) [missing](https://github.com/acme/widgets/blob/main/99%25.md)",
		);
		expect(missing).toEqual(["99%.md"]);
		expect(await findBrokenLinks(root, source.repo, [current])).toEqual([
			{ repo: source.repo, sourcePath: "index.md", target: "99%.md" },
		]);
	});
	test("handles bare percent signs in GitHub blob targets", () => {
		const input = "[x](https://github.com/acme/widgets/blob/main/100%.md)";
		expect(
			rewriteLinks(
				input,
				page("index.md", "/"),
				new Map([["acme/widgets:100%.md", "/hundred"]]),
				"/tmp/acme-widgets",
			),
		).toBe("[x](/hundred)");
	});
	test("preserves code span tags in titles", () => {
		expect(extractTitle("# Use `<div>` tag", "page.md")).toBe("Use <div> tag");
	});
	test("strips emphasis wrapping a code span in a title", () => {
		expect(extractTitle("# **`npm`** install", "page.md")).toBe("npm install");
	});
	test("strips a link wrapping a code span in a title", () => {
		expect(extractTitle("# [`foo`](./foo.md) guide", "page.md")).toBe(
			"foo guide",
		);
	});
	test("preserves generic parameters in titles", () => {
		expect(extractTitle("# Generic <T> helpers", "page.md")).toBe(
			"Generic <T> helpers",
		);
	});
	test("strips paired HTML tags and emphasis from titles", () => {
		expect(extractTitle("# **Bold** <em>x</em>", "page.md")).toBe("Bold x");
	});
	test("preserves literal underscores and angle brackets in titles", () => {
		expect(extractTitle("# snake_case", "page.md")).toBe("snake_case");
		expect(extractTitle("# List<T>", "page.md")).toBe("List<T>");
		expect(extractTitle("# **Bold** title", "page.md")).toBe("Bold title");
	});
	test("resolves root-absolute links from the source root", () => {
		const routes = new Map([["acme/widgets:docs/next.md", "/guide/next"]]);
		expect(
			rewriteLinks(
				"[x](/docs/next.md)",
				page("docs/index.md", "/guide"),
				routes,
				"/tmp/acme-widgets",
			),
		).toBe("[x](/guide/next)");
	});
	test("rewrites reference definitions outside fences", () => {
		const routes = new Map([["acme/widgets:docs/next.md", "/guide/next"]]);
		const input = "[ref]: next.md\n```\n[hidden]: missing.md\n```\n";
		expect(
			rewriteLinks(
				input,
				page("docs/index.md", "/guide"),
				routes,
				"/tmp/acme-widgets",
			),
		).toBe("[ref]: /guide/next\n```\n[hidden]: missing.md\n```\n");
	});
	test("reports broken reference definitions outside fences", async () => {
		const root = await fixture();
		await writeFile(
			join(root, "index.md"),
			"[ref]: missing.md\n```\n[hidden]: ignored.md\n```\n",
		);
		expect(
			await findBrokenLinks(root, source.repo, [page("index.md", "/")]),
		).toEqual([
			{ repo: source.repo, sourcePath: "index.md", target: "missing.md" },
		]);
	});
	test("decodes encoded paths for bundled route lookup and broken link checks", async () => {
		const root = await fixture();
		await writeFile(join(root, "index.md"), "[x](my%20doc.md)");
		await writeFile(join(root, "my doc.md"), "# Document");
		const current = page("index.md", "/");
		const target = page("my doc.md", "/my-doc");
		expect(
			rewriteLinks(
				"[x](my%20doc.md)",
				current,
				new Map([["acme/widgets:my doc.md", "/my-doc"]]),
				root,
			),
		).toBe("[x](/my-doc)");
		expect(await findBrokenLinks(root, source.repo, [current, target])).toEqual(
			[],
		);
		expect(await findBrokenLinks(root, source.repo, [current])).toEqual([]);
	});
	test("encodes blob URL segments for an existing unbundled file", async () => {
		const root = await fixture();
		await writeFile(join(root, "My Doc.md"), "# Document");
		const missing: string[] = [];
		expect(
			rewriteLinks(
				"[x](<My Doc.md>)",
				page("index.md", "/"),
				new Map(),
				root,
				(link) => missing.push(link.target),
			),
		).toBe("[x](https://github.com/acme/widgets/blob/main/My%20Doc.md)");
		expect(missing).toEqual([]);
	});
	test("rejects an allowed package README deeper than readmeDepth", async () => {
		const root = await fixture();
		await mkdir(join(root, "packages/widgets/a/b"), { recursive: true });
		await writeFile(join(root, "packages/widgets/a/b/README.md"), "# Deep");
		await expect(
			collectPages(
				{ ...source, root, config: "required" },
				{
					exclude: [],
					sections: [],
					packages: { route: "/packages", allow: ["widgets"], readmeDepth: 1 },
				},
			),
		).rejects.toThrow("stray package page: packages/widgets/a/b/README.md");
	});
	test("routes a page under a section prefix without a trailing slash", async () => {
		const root = await fixture();
		await mkdir(join(root, "docs/guide"), { recursive: true });
		await writeFile(join(root, "docs/guide/intro.md"), "# Intro");
		const pages = await collectPages(
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
		);
		expect(pages.map(({ sourcePath, route }) => [sourcePath, route])).toEqual([
			["docs/guide/intro.md", "/guide/intro"],
		]);
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
			["packages/widgets/README.md", "/packages/packages/widgets/readme"],
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
			"docs/snake_case.md",
			"docs/my_dir/page.md",
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
			"_partial.md",
			"docs/_drafts/x.md",
			"pkg/__tests__/x.md",
			"src/code.ts",
		]) {
			expect(isGatherable(path)).toBe(false);
		}
	});
	test("collectPages skips underscore-prefixed path segments", async () => {
		const root = await fixture();
		await mkdir(join(root, "_drafts"), { recursive: true });
		await writeFile(join(root, "_partial.md"), "# Partial\n");
		await writeFile(join(root, "_drafts", "draft.md"), "# Draft\n");
		await writeFile(join(root, "index.md"), "# Index\n");
		await writeFile(join(root, "snake_case.md"), "# Snake\n");
		const pages = await collectPages(
			{ ...source, root },
			{ exclude: [], sections: [] },
		);
		expect(pages.map(({ sourcePath }) => sourcePath).sort()).toEqual([
			"index.md",
			"snake_case.md",
		]);
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
			`exclude = ["private/**"]\n\n[[sections]]\nid = "guide"\nlabel = "Guide"\nroute = "/guide"\nprefix = "docs/guide/"\npagefind = false\n\n[packages]\nroute = "/packages"\nallow = ["widgets"]\nreadme_depth = 2\nnested = ["apps"]\n`,
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
			nested: ["apps"],
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
		await writeFile(join(root, "packages", "stray", "guide.md"), "# Stray\n");
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
