import { afterAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { DecisionFile } from "./index.ts";
import { listDecisions, parseArgs, run, USAGE } from "./index.ts";

const file = (
	id: string,
	{
		surface = "tools",
		area = "general",
		decision = `Decision ${id}`,
		status = "Active (Matt, 2026-10-07)",
		root = "docs/designs",
		layout = "surface",
	} = {},
): DecisionFile => ({
	path:
		layout === "surface"
			? `${root}/${surface}/decisions/${area}/${id}.md`
			: `${root}/decisions/${area}/${id}.md`,
	text: [
		"---",
		`id: ${id}`,
		`decision: "${decision}"`,
		`status: "${status}"`,
		"record: ../../rec.md#approach",
		"---",
		"",
	].join("\n"),
});

const corpus = [
	file("DL-500", { surface: "platform", area: "secrets" }),
	file("DL-002", { decision: "Use moon for task graphs" }),
	file("DL-099", {
		surface: "agents",
		status: "Superseded by DL-500 (Matt, 2026-10-08)",
	}),
	file("DL-003", { area: "ledger", status: "Retired (Matt, 2026-10-08)" }),
];
const ids = (lines: readonly string[]) =>
	lines.map((line) => line.split(" ")[0]);

describe("listDecisions", () => {
	test("one line per decision, sorted by id number", () => {
		const { lines, errors } = listDecisions(corpus, {}, { surfaceDepth: 1 });
		expect(errors).toEqual([]);
		expect(ids(lines)).toEqual(["DL-002", "DL-003", "DL-099", "DL-500"]);
	});

	test("a line carries status, location, decision and record", () => {
		const { lines } = listDecisions(
			corpus,
			{ grep: "moon" },
			{ surfaceDepth: 1 },
		);
		expect(lines).toEqual([
			"DL-002  Active  tools/general  Use moon for task graphs  → docs/designs/tools/rec.md#approach",
		]);
	});

	test("filters combine and grep searches only printed text", () => {
		expect(
			ids(
				listDecisions(corpus, { surface: "tools" }, { surfaceDepth: 1 }).lines,
			),
		).toEqual(["DL-002", "DL-003"]);
		expect(
			ids(listDecisions(corpus, { area: "ledger" }, { surfaceDepth: 1 }).lines),
		).toEqual(["DL-003"]);
		expect(
			ids(
				listDecisions(
					corpus,
					{ status: "active", surface: "tools" },
					{ surfaceDepth: 1 },
				).lines,
			),
		).toEqual(["DL-002"]);
		expect(
			ids(
				listDecisions(corpus, { grep: "TASK GRAPH" }, { surfaceDepth: 1 })
					.lines,
			),
		).toEqual(["DL-002"]);
		expect(
			listDecisions(corpus, { grep: "Matt" }, { surfaceDepth: 1 }).lines,
		).toEqual([]);
		expect(
			listDecisions(corpus, { grep: "../" }, { surfaceDepth: 1 }).lines,
		).toEqual([]);
	});

	test("a layout without a surface level lists the area alone", () => {
		const flat = file("DL-010", { area: "ci", layout: "flat" });
		const { lines } = listDecisions(
			[flat],
			{ area: "ci" },
			{ surfaceDepth: 0 },
		);
		expect(lines[0]).toContain("  ci  ");
	});

	test("a malformed file is reported, not dropped silently", () => {
		const bad: DecisionFile = {
			path: "docs/designs/tools/decisions/general/DL-004.md",
			text: "no front matter\n",
		};
		const { lines, errors } = listDecisions(
			[...corpus, bad],
			{},
			{ surfaceDepth: 1 },
		);
		expect(lines.length).toBe(4);
		expect(errors).toEqual([
			"docs/designs/tools/decisions/general/DL-004.md:1: file must start with a `---` front-matter block",
		]);
	});
});

describe("parseArgs", () => {
	test("defaults to the consumer config and accepts filters", () => {
		expect(
			parseArgs([
				"--surface",
				"tools",
				"--area=ci",
				"--status",
				"Active",
				"--grep",
				"moon",
			]),
		).toEqual({
			filters: { surface: "tools", area: "ci", status: "active", grep: "moon" },
			configPath: "design-ledger.config.json",
			glob: undefined,
			help: false,
		});
		expect(
			parseArgs(["--config", "./ledger.json", "--glob", "x/*.md"]).configPath,
		).toBe("./ledger.json");
		expect(parseArgs(["--glob=x/*.md"]).glob).toBe("x/*.md");
	});

	test("rejects bad input", () => {
		expect(() => parseArgs(["--status", "live"])).toThrow(
			"--status must be one of active, superseded, retired",
		);
		expect(() => parseArgs(["--bogus"])).toThrow("unknown argument --bogus");
		expect(() => parseArgs(["--grep"])).toThrow("--grep needs a value");
		expect(() => parseArgs(["--grep", "--surface", "x"])).toThrow(
			"--grep needs a value",
		);
		expect(() => parseArgs(["--area="])).toThrow("--area needs a value");
		expect(() => parseArgs(["--config"])).toThrow("--config needs a value");
		expect(() => parseArgs(["--config", "a", "--config=b"])).toThrow(
			"--config may appear once",
		);
		expect(parseArgs(["--grep=--no-hooks"]).filters.grep).toBe("--no-hooks");
	});
});

describe("run", () => {
	const roots: string[] = [];
	afterAll(() => {
		for (const root of roots) rmSync(root, { recursive: true, force: true });
	});
	const tree = (
		files: readonly DecisionFile[],
		config: {
			designsRoot: string;
			surfaceDepth: 0 | 1;
			surfaces?: readonly string[];
		},
	): string => {
		const root = mkdtempSync(join(tmpdir(), "dl-list-"));
		roots.push(root);
		const ledgerConfig = {
			...config,
			...(config.surfaceDepth === 0 ? { governedRoots: ["ui"] } : {}),
			counter: { url: "https://counter.example.test", partition: "test" },
		};
		writeFileSync(
			join(root, "design-ledger.config.json"),
			JSON.stringify(ledgerConfig),
		);
		for (const entry of files) {
			mkdirSync(join(root, entry.path, ".."), { recursive: true });
			writeFileSync(join(root, entry.path), entry.text);
		}
		return root;
	};
	const call = async (
		argv: string[],
		root: string,
		readDecisionFile?: (path: string) => Promise<string>,
	) => {
		const out: string[] = [];
		const err: string[] = [];
		const code = await run(
			argv,
			root,
			{
				out: (line) => out.push(line),
				err: (line) => err.push(line),
			},
			readDecisionFile,
		);
		return { code, out, err };
	};

	test("uses config-driven roots for orion-docs and compass-docs layouts", async () => {
		const orionDocs = tree(corpus, {
			designsRoot: "docs/designs",
			surfaceDepth: 1,
		});
		const compassDocs = tree([file("DL-010", { area: "ci", layout: "flat" })], {
			designsRoot: "docs/designs",
			surfaceDepth: 0,
		});
		const orion = await call([], orionDocs);
		const compass = await call([], compassDocs);
		expect([orion.code, ids(orion.out), orion.err]).toEqual([
			0,
			["DL-002", "DL-003", "DL-099", "DL-500"],
			[],
		]);
		expect([compass.code, ids(compass.out), compass.err]).toEqual([
			0,
			["DL-010"],
			[],
		]);
	});
	test("a non-default designsRoot drives the default glob", async () => {
		const root = tree(
			[file("DL-011", { area: "ci", root: "ledger-tree", layout: "flat" })],
			{ designsRoot: "ledger-tree", surfaceDepth: 0 },
		);
		const result = await call([], root);
		expect([result.code, ids(result.out), result.err]).toEqual([
			0,
			["DL-011"],
			[],
		]);
	});

	test("depth-zero surface filtering requires one configured surface", async () => {
		const root = tree([file("DL-012", { area: "ci", layout: "flat" })], {
			designsRoot: "docs/designs",
			surfaceDepth: 0,
		});
		const result = await call(["--surface", "compass"], root);
		expect(result.code).toBe(2);
		expect(result.err[0]).toContain("requires exactly one configured surface");
	});
	test("a decision file read error is reported with exit 2", async () => {
		const root = tree([file("DL-013", { area: "ci", layout: "flat" })], {
			designsRoot: "docs/designs",
			surfaceDepth: 0,
		});
		const result = await call([], root, async () => {
			throw new Error("EACCES");
		});
		expect(result.code).toBe(2);
		expect(result.err[0]).toContain("EACCES");
	});

	test("depth-zero surface filtering selects the configured surface", async () => {
		const root = tree([file("DL-014", { area: "ci", layout: "flat" })], {
			designsRoot: "docs/designs",
			surfaceDepth: 0,
			surfaces: ["compass"],
		});
		const result = await call(["--surface", "compass"], root);
		expect([result.code, ids(result.out), result.err]).toEqual([
			0,
			["DL-014"],
			[],
		]);
	});

	test("an explicit glob overrides the layout-derived pattern", async () => {
		const root = tree(
			[file("DL-010", { area: "ci", root: "ledger", layout: "flat" })],
			{ designsRoot: "docs/designs", surfaceDepth: 0 },
		);
		const result = await call(["--glob", "ledger/decisions/*/DL-*.md"], root);

		expect([result.code, ids(result.out), result.err]).toEqual([
			0,
			["DL-010"],
			[],
		]);
	});

	test("a glob matching nothing fails instead of looking like an empty ledger", async () => {
		const root = tree([file("DL-010", { area: "ci", layout: "flat" })], {
			designsRoot: "docs/designs",
			surfaceDepth: 0,
		});
		const result = await call(["--glob", "missing/*.md"], root);
		expect(result.code).toBe(1);
		expect(result.err[0]).toContain("no decision files match");
	});

	test("a filter matching nothing still succeeds", async () => {
		const root = tree(corpus, { designsRoot: "docs/designs", surfaceDepth: 1 });
		const result = await call(["--grep", "no such text"], root);
		expect([result.code, result.out, result.err]).toEqual([0, [], []]);
	});

	test("a malformed file prints the rest and exits 1", async () => {
		const root = tree(
			[
				...corpus,
				{ path: "docs/designs/tools/decisions/general/DL-004.md", text: "x\n" },
			],
			{ designsRoot: "docs/designs", surfaceDepth: 1 },
		);
		const result = await call([], root);
		expect([result.code, result.out.length, result.err.length]).toEqual([
			1, 4, 1,
		]);
	});

	test("help goes to stdout with exit 0; a usage error exits 2", async () => {
		const root = tree(corpus, { designsRoot: "docs/designs", surfaceDepth: 1 });
		expect(await call(["--help"], root)).toEqual({
			code: 0,
			out: [USAGE],
			err: [],
		});
		const bad = await call(["--bogus"], root);
		expect([bad.code, bad.out]).toEqual([2, []]);
	});

	test("configuration read errors use usage exit 2", async () => {
		const root = mkdtempSync(join(tmpdir(), "dl-list-config-"));
		roots.push(root);
		const result = await call([], root);
		expect(result.code).toBe(2);
		expect(result.err[0]).toContain("Cannot load config");
	});
});
