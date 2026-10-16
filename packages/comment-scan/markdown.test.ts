import { describe, expect, test } from "bun:test";
import { compareMarkdown, markdownFingerprint, markdownScan } from "./index.ts";

function changesStructure(before: string, after: string): boolean {
	return compareMarkdown(before, after).kind !== "same";
}

describe("Markdown structure fingerprinting", () => {
	test("rewording prose preserves structure", () => {
		expect(
			changesStructure(
				"# Title\n\nFirst sentence.\nIt continues.",
				"# Title\n\nReworded sentence.",
			),
		).toBe(false);
	});

	test("fenced code and fence info remain structural", () => {
		expect(
			changesStructure("```bash\necho old\n```", "```bash\necho new\n```"),
		).toBe(true);
		expect(changesStructure("```bash\nrun()\n```", "```sh\nrun()\n```")).toBe(
			true,
		);
	});

	test("fence content is not parsed as headings", () => {
		const before = "Intro prose.\n\n```py\n# payload\nx = 1\n```";
		expect(
			markdownFingerprint(before).some((item) => item.kind === "heading"),
		).toBe(false);
		expect(
			changesStructure(
				before,
				"Intro changed.\n\n```py\n# payload\nx = 1\n```",
			),
		).toBe(false);
	});

	test("editing a hash line inside a fence changes structure", () => {
		expect(
			changesStructure("```py\n# keep me\n```", "```py\n# changed\n```"),
		).toBe(true);
	});

	test("heading text and level are structural", () => {
		expect(changesStructure("## Install steps", "## Setup steps")).toBe(true);
		expect(changesStructure("## Install", "### Install")).toBe(true);
	});

	test("link destinations are structural but link text is prose", () => {
		expect(
			changesStructure("See [docs](./old.md).", "See [docs](./new.md)."),
		).toBe(true);
		expect(
			changesStructure("See [docs](./x.md).", "See [guide](./x.md)."),
		).toBe(false);
	});

	test("inline code spans and rumdl directives are structural", () => {
		expect(changesStructure("Run `oldCmd` now.", "Run `newCmd` now.")).toBe(
			true,
		);
		expect(
			changesStructure(
				"<!-- rumdl-disable MD013 -->",
				"<!-- rumdl-disable MD033 -->",
			),
		).toBe(true);
	});

	test("ordinary HTML comments are prose", () => {
		expect(
			changesStructure(
				"<!-- note -->\n\nprose",
				"<!-- another note -->\n\nreworded prose",
			),
		).toBe(false);
	});

	test("front matter and indented code are structural", () => {
		expect(
			changesStructure(
				"---\ntitle: A\n---\nbody",
				"---\ntitle: B\n---\nreworded",
			),
		).toBe(true);
		expect(
			changesStructure("prose\n\n    code one", "prose\n\n    code two"),
		).toBe(true);
	});

	test("reference destinations and autolinks are structural", () => {
		expect(
			changesStructure(
				"[tool][t]\n\n[t]: https://a.example",
				"[tool][t]\n\n[t]: https://b.example",
			),
		).toBe(true);
		expect(changesStructure("<https://a.example>", "<https://b.example>")).toBe(
			true,
		);
	});

	test("tables and Setext heading levels are structural", () => {
		expect(
			changesStructure("| A | B |\n| --- | --- |", "| X | B |\n| --- | --- |"),
		).toBe(true);
		expect(changesStructure("Title\n====", "Title\n----")).toBe(true);
	});

	test("comparison reports the changed heading entries", () => {
		expect(compareMarkdown("## Install", "## Setup")).toMatchObject({
			kind: "mismatch",
			before: { kind: "heading", value: "H2 Install" },
			after: { kind: "heading", value: "H2 Setup" },
		});
	});

	test("an unclosed comment is malformed", () => {
		expect(markdownScan("<!-- unfinished").defect).toBe(
			"an HTML comment opened and never closed",
		);
	});

	test("unclosed front matter and fences are malformed", () => {
		expect(markdownScan("---\ntitle: A").defect).toBe(
			"front matter opened with `---` and never closed",
		);
		expect(markdownScan("```\ncode").defect).toBe(
			"a fenced code block opened and never closed",
		);
	});

	test("a malformed side is reported before structural comparison", () => {
		expect(compareMarkdown("<!-- unfinished", "<!-- changed")).toMatchObject({
			kind: "malformed",
			side: "before",
		});
	});

	test("bare URLs and raw HTML attributes are structural", () => {
		expect(
			changesStructure(
				"see https://a.example/x here",
				"see https://b.example/x here",
			),
		).toBe(true);
		expect(changesStructure('<a href="/a">x</a>', '<a href="/b">x</a>')).toBe(
			true,
		);
	});
});
