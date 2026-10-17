import { describe, expect, test } from "bun:test";
import type { FileDiff } from "@rigelbuild/comment-scan";
import {
	CEILING,
	commentMarker,
	docMarker,
	type Finding,
	formatFinding,
	judgeFile,
	type LengthGateConfig,
	lineMap,
	MARKER,
	MIN_REASON_WORDS,
	proseWords,
} from "./index.ts";

const COMMENT_CONFIG: LengthGateConfig = {
	extensions: [".ts"],
	exclude: [],
	docs: [],
};

const DOC_CONFIG: LengthGateConfig = {
	extensions: [".ts"],
	exclude: [],
	docs: [
		{
			name: "spec",
			include: ["docs/specs/**/*.md"],
			exclude: [],
			target: 1500,
			markerAllowed: false,
		},
		{
			name: "design record",
			include: ["docs/designs/**/*.md"],
			exclude: [],
			target: 2500,
			markerAllowed: true,
		},
		{
			name: "repo doc",
			include: ["docs/**/*.md"],
			exclude: ["docs/designs/**"],
			target: 1500,
			markerAllowed: true,
		},
	],
};

function diffFile(
	path: string,
	changedLines: readonly number[] = [],
): FileDiff {
	return {
		path,
		oldPath: path,
		newPath: path,
		hunks: changedLines.length
			? [
					{
						lines: changedLines.map((newNo) => ({
							kind: "+" as const,
							text: "",
							oldNo: 0,
							newNo,
						})),
					},
				]
			: [],
		added: false,
		deleted: false,
		binary: false,
		metadataOnly: false,
	};
}

function judgeComment(
	before: string,
	after: string,
	changedLines: readonly number[] = [],
) {
	return judgeFile(COMMENT_CONFIG, diffFile("src/file.ts", changedLines), {
		before,
		after,
	});
}

function comments(count: number, indent = "", label = "line"): string {
	return Array.from(
		{ length: count },
		(_, index) => `${indent}// ${label} ${index + 1}`,
	).join("\n");
}

function words(count: number): string {
	return Array.from({ length: count }, (_, index) => `word${index + 1}`).join(
		" ",
	);
}

function designRecord(body: string): string {
	return `# Record\n\n${body}`;
}

function judgeDoc(
	path: string,
	before: string,
	after: string,
	changedLines: readonly number[] = [],
	config = DOC_CONFIG,
) {
	return judgeFile(config, diffFile(path, changedLines), { before, after });
}

describe("fixed policy and marker parsers", () => {
	test("exports the fixed comment and marker policy", () => {
		expect(CEILING).toBe(4);
		expect(MARKER).toBe("LENGTH");
		expect(MIN_REASON_WORDS).toBe(3);
	});

	test("parses source comment markers and requires three reason words", () => {
		expect(commentMarker("// LENGTH: bit layout of the header")).toEqual({
			reason: "bit layout of the header",
		});
		expect(commentMarker("# LENGTH: why")).toEqual({
			error: expect.stringContaining("3 words"),
		});
		expect(commentMarker("// ordinary note")).toBeNull();
	});

	test("parses doc markers, checks reason length, and rejects missing sections", () => {
		expect(
			docMarker(
				"---\ntitle: Record\n---\n<!-- LENGTH §Plan: per-task interfaces readers need -->\n\n## Plan\n",
			),
		).toEqual({ section: "Plan", reason: "per-task interfaces readers need" });
		expect(docMarker("<!-- LENGTH §Plan: why -->\n## Plan")).toEqual({
			error: expect.stringContaining("3 words"),
		});
		expect(
			docMarker("<!-- LENGTH §Nope: valid reason names section -->\n## Plan"),
		).toEqual({ error: expect.stringContaining("Nope") });
		expect(docMarker("## Plan\nNo marker")).toBeNull();
	});
});

describe("comment ratchet", () => {
	test("a new five-line block fails, while a valid live marker passes", () => {
		const unmarked = judgeComment("", comments(5));
		expect(unmarked.findings).toContainEqual({
			kind: "comment",
			path: "src/file.ts",
			line: 1,
			lines: 5,
			baseLines: 0,
		});
		const markedText = `// LENGTH: bit layout of the header\n${comments(4)}`;
		const marked = judgeComment("", markedText, [1]);
		expect(marked.findings).toEqual([]);
	});

	test("a short comment marker is a bad-marker finding", () => {
		const result = judgeComment("", `// LENGTH: why\n${comments(4)}`, [1]);
		expect(result.findings).toContainEqual({
			kind: "bad-marker",
			path: "src/file.ts",
			line: 1,
			reason: expect.stringContaining("3 words"),
		});
	});

	test("a ten-line block cut to eight passes and growth reports its base length", () => {
		expect(judgeComment(comments(10), comments(8)).findings).toEqual([]);
		expect(judgeComment(comments(10), comments(11)).findings).toContainEqual({
			kind: "comment",
			path: "src/file.ts",
			line: 1,
			lines: 11,
			baseLines: 10,
		});
	});

	test("joining two three-line blocks into six fails", () => {
		const before = `${comments(3)}\n\n${comments(3, "", "other")}`;
		const result = judgeComment(
			before,
			`${comments(3)}\n${comments(3, "", "other")}`,
		);
		expect(result.findings).toContainEqual({
			kind: "comment",
			path: "src/file.ts",
			line: 1,
			lines: 6,
			baseLines: 3,
		});
	});

	test("an untouched long block in a changed file passes", () => {
		const before = `${comments(6)}\n\nconst oldValue = 1;`;
		const after = `${comments(6)}\n\nconst newValue = 1;`;
		expect(judgeComment(before, after).findings).toEqual([]);
	});

	test("reindent, same-file movement, and a full reworded trim pass", () => {
		const base = comments(10);
		expect(judgeComment(base, comments(10, "    ")).findings).toEqual([]);
		expect(
			judgeComment(`${base}\n\nconst value = 1;`, `const value = 1;\n\n${base}`)
				.findings,
		).toEqual([]);
		expect(judgeComment(base, comments(8, "", "rewritten")).findings).toEqual(
			[],
		);
	});

	test("a trim can fund a new long block in the same file", () => {
		const before = comments(10);
		const after = `${comments(4)}\n\n${comments(6, "", "new")}`;
		expect(judgeComment(before, after).findings).toEqual([]);
	});

	test("a long block moved into a new file fails", () => {
		const result = judgeFile(COMMENT_CONFIG, diffFile("src/new.ts", [1]), {
			before: "",
			after: comments(5),
		});
		expect(result.findings).toContainEqual({
			kind: "comment",
			path: "src/new.ts",
			line: 1,
			lines: 5,
			baseLines: 0,
		});
		expect(judgeComment("", comments(5)).findings).toHaveLength(1);
	});

	test("a marker line must change before it exempts a growing comment block", () => {
		const before = `// LENGTH: preserve old protocol details\n${comments(9)}`;
		const unchanged = `// LENGTH: preserve old protocol details\n${comments(10)}`;
		const edited = `// LENGTH: document new protocol details\n${comments(10)}`;
		expect(judgeComment(before, unchanged, [11]).findings).toContainEqual({
			kind: "comment",
			path: "src/file.ts",
			line: 1,
			lines: 11,
			baseLines: 10,
		});
		expect(judgeComment(before, edited, [1, 11]).findings).toEqual([]);
	});

	test("excluded and generated-header files are not judged", () => {
		const excluded = judgeFile(
			{ ...COMMENT_CONFIG, exclude: ["vendor/**"] },
			diffFile("vendor/generated.ts", [1]),
			{ before: "", after: comments(8) },
		);
		const generated = judgeComment(
			"",
			`// Code generated by tool. DO NOT EDIT.\n${comments(7)}`,
		);
		expect(excluded).toEqual({ findings: [], notes: [] });
		expect(generated.findings).toEqual([]);
	});

	test("ambiguous source is unscannable and clean source passes", () => {
		expect(judgeComment("", 'const value = "unfinished').findings).toEqual([
			{ kind: "unscannable", path: "src/file.ts" },
		]);
		expect(judgeComment("", "const value = 1;").findings).toEqual([]);
	});

	test("the first matching doc class wins", () => {
		const result = judgeDoc("docs/designs/tools/record.md", "", words(20), [], {
			...DOC_CONFIG,
			docs: [
				{
					name: "first",
					include: ["docs/**/*.md"],
					exclude: [],
					target: 100,
					markerAllowed: true,
				},
				{
					name: "second",
					include: ["docs/designs/**/*.md"],
					exclude: [],
					target: 10,
					markerAllowed: true,
				},
			],
		});
		expect(result.findings).toEqual([]);
	});
});

describe("doc ratchet", () => {
	test("a new 2,600-word design record fails and a live valid marker passes", () => {
		const unmarked = judgeDoc(
			"docs/designs/tools/record.md",
			"",
			designRecord(words(2599)),
		);
		expect(unmarked.findings).toContainEqual({
			kind: "doc",
			path: "docs/designs/tools/record.md",
			docClass: "design record",
			words: 2600,
			baseWords: 0,
			target: 2500,
		});
		const markedText = `<!-- LENGTH §Plan: per-task interfaces readers need -->\n\n${designRecord(`## Plan\n\n${words(2599)}`)}`;
		expect(
			judgeDoc("docs/designs/tools/record.md", "", markedText, [1]).findings,
		).toEqual([]);
	});

	test("a doc marker naming a missing section is bad-marker", () => {
		const text = `<!-- LENGTH §Nope: per-task interfaces readers need -->\n\n## Plan\n\n${words(2600)}`;
		const result = judgeDoc("docs/designs/tools/record.md", "", text, [1]);
		expect(result.findings).toContainEqual({
			kind: "bad-marker",
			path: "docs/designs/tools/record.md",
			line: 1,
			reason: expect.stringContaining("Nope"),
		});
	});

	test("a 3,000-word record trimmed to 2,900 passes", () => {
		const before = designRecord(words(3000));
		const after = designRecord(words(2900));
		expect(
			judgeDoc("docs/designs/tools/record.md", before, after).findings,
		).toEqual([]);
	});

	test("front matter, fences, HTML comments, tables, links, and Errata are excluded", () => {
		const markdown = [
			"---",
			`title: ${words(20)}`,
			"---",
			`Counted ${words(2)} [label](https://example.test/target) and [ref][destination].`,
			"[destination]: https://example.test/reference-target",
			"```ts",
			words(40),
			"```",
			"<!--",
			words(30),
			"-->",
			"| Heading | More |",
			"| --- | --- |",
			`| ${words(20)} | ${words(20)} |`,
			"| Single column |",
			"| --- |",
			"| hidden words |",
			"## Errata",
			words(50),
		].join("\n");
		expect(proseWords(markdown, true)).toBe(6);
	});

	test("punctuation tokens count only when they contain a letter or digit", () => {
		expect(proseWords("foo-bar/baz x.y -- ->", false)).toBe(2);
	});

	test("an excluded path is final and a class exclusion permits a later class match", () => {
		const globallyExcluded = judgeDoc(
			"docs/specs/private.md",
			"",
			words(1600),
			[],
			{ ...DOC_CONFIG, exclude: ["docs/**"] },
		);
		const classFallback = judgeDoc(
			"docs/designs/tools/record.md",
			"",
			words(20),
			[],
			{
				...DOC_CONFIG,
				docs: [
					{
						name: "skipped",
						include: ["docs/**/*.md"],
						exclude: ["docs/designs/**"],
						target: 10,
						markerAllowed: true,
					},
					{
						name: "fallback",
						include: ["docs/designs/**/*.md"],
						exclude: [],
						target: 10,
						markerAllowed: true,
					},
				],
			},
		);
		expect(globallyExcluded).toEqual({ findings: [], notes: [] });
		expect(classFallback.findings).toContainEqual({
			kind: "doc",
			path: "docs/designs/tools/record.md",
			docClass: "fallback",
			words: 20,
			baseWords: 0,
			target: 10,
		});
	});

	test("an unchanged doc marker is not live, but editing its reason makes it live", () => {
		const before = `<!-- LENGTH §Plan: preserve the old interface details -->\n\n## Plan\n\n${words(2500)}`;
		const unchanged = `<!-- LENGTH §Plan: preserve the old interface details -->\n\n## Plan\n\n${words(2501)}`;
		const edited = `<!-- LENGTH §Plan: explain the new interface details -->\n\n## Plan\n\n${words(2501)}`;
		expect(
			judgeDoc("docs/designs/tools/record.md", before, unchanged, [5]).findings,
		).toContainEqual({
			kind: "doc",
			path: "docs/designs/tools/record.md",
			docClass: "design record",
			words: 2502,
			baseWords: 2501,
			target: 2500,
		});
		expect(
			judgeDoc("docs/designs/tools/record.md", before, edited, [1, 5]).findings,
		).toEqual([]);
	});

	test("a disallowed doc marker is reported and does not exempt an over-target spec", () => {
		const text = `<!-- LENGTH §Plan: split the chapters across files -->\n\n## Plan\n\n${words(1599)}`;
		const result = judgeDoc("docs/specs/gate.md", "", text, [1]);
		expect(result.findings).toContainEqual({
			kind: "bad-marker",
			path: "docs/specs/gate.md",
			line: 1,
			reason: expect.stringContaining("not allowed"),
		});
		expect(result.findings).toContainEqual({
			kind: "doc",
			path: "docs/specs/gate.md",
			docClass: "spec",
			words: 1600,
			baseWords: 0,
			target: 1500,
		});
	});

	test("a marker below the comment ceiling produces an unneeded note", () => {
		const text = "// LENGTH: preserve bit layout details\n// one\n// two";
		const result = judgeComment("", text, [1]);
		expect(result.findings).toEqual([]);
		expect(result.notes).toEqual([
			{ kind: "unneeded-marker", path: "src/file.ts", line: 1 },
		]);
	});
});

describe("line pairing and failure output", () => {
	test("lineMap pairs trimmed equal lines using the diff alignment", () => {
		const map = lineMap("first\n  stable\nlast", "new\n\tstable\nlast");
		expect(map(1)).toBeNull();
		expect(map(2)).toBe(2);
		expect(map(3)).toBe(3);
	});

	test("spec output names the target and recommends splitting instead of a marker", () => {
		const finding: Finding = {
			kind: "doc",
			path: "docs/specs/gate.md",
			docClass: "spec",
			words: 1600,
			baseWords: 0,
			target: 1500,
		};
		const lines = formatFinding(finding);
		expect(lines[0]).toContain("(spec target 1,500)");
		expect(lines[1]?.toLowerCase()).toContain("split");
		expect(lines[1]?.toLowerCase()).not.toContain("marker");
	});

	test("bad-marker and unscannable output contain the finding and fix lines", () => {
		const bad: Finding = {
			kind: "bad-marker",
			path: "src/file.ts",
			line: 4,
			reason: "reason must have 3 words",
		};
		const unscannable: Finding = { kind: "unscannable", path: "src/file.ts" };
		expect(formatFinding(bad)).toEqual([
			"src/file.ts:4: bad LENGTH marker: reason must have 3 words",
			expect.stringContaining("Fix the reason"),
		]);
		expect(formatFinding(unscannable)).toEqual([
			"src/file.ts: no scanner could place comments",
			expect.stringContaining("add scanner support"),
		]);
	});
});
