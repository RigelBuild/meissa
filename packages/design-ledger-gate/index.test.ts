import { afterEach, describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	buildDecisionCorpus,
	type Changed,
	classifyDesignPath,
	conflictMarkerViolations,
	type DecisionCorpus,
	type Deps,
	evaluate,
	KEY_LINE,
	type LedgerConfig,
	loadLedgerConfig,
	parseRecordHeader,
	parseStatusValue,
	prContextFrom,
	type RecordContent,
	type RecordHeader,
	recordContentFromText,
	resolveRecordRelative,
	runOnce,
	slugify,
	touchesRecord,
	type Violation,
} from "./index.ts";

const CONFIG: LedgerConfig = {
	designsRoot: "docs/designs",
	surfaceDepth: 0,
	governedRoots: [
		"ui",
		"agent",
		"server",
		"meta",
		"infra",
		"observability",
		"repo",
		"gamma",
	],
	counter: { url: "https://example.invalid/counter", partition: "test" },
};
const DECISION_DIR = "docs/designs/decisions";
const noChange: Changed = { files: [], body: null, headBranch: "" };
const smallRecord = (): RecordContent => ({
	headings: ["present"],
	sizeBytes: 100,
});
const temporaryDirectories: string[] = [];
function temporaryDirectory(): string {
	const path = mkdtempSync(join(tmpdir(), "design-ledger-"));
	temporaryDirectories.push(path);
	return path;
}
afterEach(() => {
	for (const path of temporaryDirectories.splice(0))
		rmSync(path, { recursive: true, force: true });
});

interface DecisionInput {
	id?: string;
	area?: string;
	record?: string;
	status?: string;
	decision?: string;
	path?: string;
}

function decisionText(options: DecisionInput = {}): string {
	const id = options.id ?? "DL-001";
	const area = options.area ?? "ui";
	const record = options.record ?? `../../${area}/record/design.md`;
	const status = options.status ?? "Active (Matt, 2026-07-22)";
	const decision = options.decision ?? "Use the stable design.";
	return [
		"---",
		`id: ${id}`,
		`decision: ${JSON.stringify(decision)}`,
		`status: ${JSON.stringify(status)}`,
		`record: ${record}`,
		"---",
		"# Decision",
		"",
		decision,
		"",
	].join("\n");
}

function decisionFile(options: DecisionInput = {}) {
	const id = options.id ?? "DL-001";
	const area = options.area ?? "ui";
	return {
		path: options.path ?? `${DECISION_DIR}/${area}/${id}.md`,
		text: decisionText(options),
	};
}

function corpus(...files: DecisionInput[]): DecisionCorpus {
	const inputs = files.length === 0 ? [{}] : files;
	return buildDecisionCorpus(inputs.map((file) => decisionFile(file)));
}

function record(
	path = "docs/designs/ui/record/design.md",
	text = "# Title\n",
): RecordHeader {
	return parseRecordHeader(path, text);
}
function evaluateCorpus(
	decisions: DecisionCorpus,
	records: RecordHeader[] = [],
	changed: Changed = noChange,
	read: (path: string) => RecordContent | null = smallRecord,
	config: LedgerConfig = CONFIG,
): Violation[] {
	return evaluate(decisions, records, changed, read, config);
}

test("slugify matches GitHub punctuation and whitespace rules", () => {
	expect(slugify("Hello, world!")).toBe("hello-world");
	expect(slugify("Problem / Intent")).toBe("problem--intent");
	expect(slugify("Approach — part 1")).toBe("approach--part-1");
});

test.each([
	["blockquoted Historical", "> Status: Historical", { kind: "Historical" }],
	["bold Historical", "**Status: Historical**", { kind: "Historical" }],
	[
		"quoted bold Historical",
		"> **Status: Historical**",
		{ kind: "Historical" },
	],
	[
		"bold supersession",
		"**Status: Superseded by ../next/design.md**",
		{ kind: "Superseded", path: "../next/design.md" },
	],
	[
		"case-insensitive key and free-text tail",
		"Status: Superseded by foo.md for wave runtime, pane placement…",
		{ kind: "Superseded", path: "foo.md" },
	],
	["bold key", "**Status**: Historical", { kind: "Historical" }],
	["spaced colon", "Status : Historical", { kind: "Historical" }],
] as const)("parseStatusValue accepts %s", (_label, line, result) => {
	expect(parseStatusValue(line)).toEqual(result);
});

test.each(["Status: Draft", "Status: Active", "**Status: Active**"])(
	"parseStatusValue rejects %s",
	(line) => expect(parseStatusValue(line)).toBeNull(),
);

test.each(["**Status**: Draft", "Status : Draft"])(
	"parseRecordHeader exposes prohibited values in %s",
	(line) => {
		const parsed = parseRecordHeader("record.md", `# Title\n\n${line}\n`);
		expect(parsed.statusLine).toBe(line);
		expect(parseStatusValue(parsed.statusLine ?? "")).toBeNull();
	},
);

test("record-relative supersession resolves nested and cross-bucket paths only", () => {
	expect(
		resolveRecordRelative("ui/nested/record/design.md", "../sibling/design.md"),
	).toBe("ui/nested/sibling/design.md");
	expect(
		resolveRecordRelative("ui/record/design.md", "../../server/next/design.md"),
	).toBe("server/next/design.md");
	expect(resolveRecordRelative("ui/record.md", "other.md")).toBe("ui/other.md");
	expect(
		resolveRecordRelative("ui/record/design.md", "../../../escape.md"),
	).toBeNull();
});

describe("parseRecordHeader", () => {
	test("finds the Status slot and ignores preamble, missing H1, and fenced H1", () => {
		expect(
			parseRecordHeader(
				"record.md",
				"Preamble\n# Title\n\nStatus: Historical\n",
			),
		).toEqual({
			path: "record.md",
			statusLine: "Status: Historical",
			line: 4,
		});
		expect(parseRecordHeader("record.md", "Preamble only\n")).toEqual({
			path: "record.md",
			statusLine: null,
			line: 1,
		});
		expect(
			parseRecordHeader("record.md", "```md\n# Example\n```\nPreamble\n"),
		).toEqual({
			path: "record.md",
			statusLine: null,
			line: 1,
		});
		expect(parseRecordHeader("record.md", "# Title\n\nBody\n")).toEqual({
			path: "record.md",
			statusLine: null,
			line: 2,
		});
	});

	test.each([
		"> Status: Historical",
		"**Status: Historical**",
		"> **Status: Historical**",
	])("finds blockquoted or emphasized header %s", (line) => {
		expect(
			parseRecordHeader("record.md", `# Title\n\n${line}\n`).statusLine,
		).toBe(line);
	});
});
describe("design path discovery", () => {
	test("classifies decision files, the readme, legacy files, and misplaced files", () => {
		expect(classifyDesignPath(`${DECISION_DIR}/README.md`, CONFIG)).toBe(
			"decisions-readme",
		);
		expect(classifyDesignPath(`${DECISION_DIR}/ui/DL-001.md`, CONFIG)).toBe(
			"decision",
		);
		expect(
			classifyDesignPath(`${DECISION_DIR}/ui/nested/DL-001.md`, CONFIG),
		).toBe("misplaced");
		expect(classifyDesignPath(`${DECISION_DIR}/ui/notes.txt`, CONFIG)).toBe(
			"misplaced",
		);
		expect(classifyDesignPath(`${DECISION_DIR}/DL-001.md`, CONFIG)).toBe(
			"misplaced",
		);
		expect(classifyDesignPath(`${DECISION_DIR}/foo.md`, CONFIG)).toBe(
			"misplaced",
		);
		expect(classifyDesignPath(`${DECISION_DIR}/ui/DECISIONS.md`, CONFIG)).toBe(
			"legacy-ledger",
		);
		expect(classifyDesignPath("docs/designs/DECISIONS.md", CONFIG)).toBe(
			"legacy-ledger",
		);
		expect(classifyDesignPath("docs/designs/server/DECISIONS.md", CONFIG)).toBe(
			"legacy-ledger",
		);
		expect(classifyDesignPath("docs/designs/ui/DL-009.md", CONFIG)).toBe(
			"misplaced",
		);
		expect(classifyDesignPath("docs/designs/ui/record/design.md", CONFIG)).toBe(
			"other",
		);
	});

	test("decision tree is not a governed record", () => {
		expect(touchesRecord(`${DECISION_DIR}/ui/DL-001.md`, CONFIG)).toBe(false);
		expect(touchesRecord("docs/designs/DECISIONS.md", CONFIG)).toBe(false);
	});
});

describe("touchesRecord", () => {
	test.each([
		["docs/designs/ui/example.md", true],
		["docs/designs/ui/example/design.md", true],
		["docs/designs/ui/example/other.md", true],
		["docs/designs/infra/runtime/sample/microvm-v3.md", true],
		["docs/designs/gamma/x/design.md", true],
		["docs/designs/CONTRIBUTING.md", false],
		["docs/designs/nope/example.md", false],
		["docs/designs/ui/subgroup/flat.md", true],
		["docs/designs/not-governed/record/design.md", false],
		["docs/designs/ui/", false],
		["docs/not-designs/ui/record/design.md", false],
		["docs/designs/ui/record/image.png", false],
	])("%s -> %s", (path, expected) => {
		expect(touchesRecord(path, CONFIG)).toBe(expected);
	});
});

describe("decision parsing and invariants", () => {
	test("Retired decisions pass and never require a successor", () => {
		expect(
			evaluateCorpus(corpus({ status: "Retired (Matt, 2026-08-23)" })),
		).toEqual([]);
		expect(
			evaluateCorpus(
				corpus(
					{ id: "DL-001", status: "Retired (Matt, 2026-08-23)" },
					{ id: "DL-002", status: "Retired (Matt, 2026-08-23)" },
				),
			),
		).toEqual([]);
	});

	test("decision status grammar is enforced by the parser at line 4", () => {
		const malformed = buildDecisionCorpus([
			{ ...decisionFile(), text: decisionText({ status: "Draft" }) },
		]);
		expect(malformed.rows).toHaveLength(0);
		expect(malformed.malformed[0]).toMatchObject({
			path: `${DECISION_DIR}/ui/DL-001.md`,
			line: KEY_LINE.status,
		});
	});

	test("duplicate ids across areas point to the second id key", () => {
		const got = evaluateCorpus(
			corpus(
				{ id: "DL-001", area: "ui" },
				{
					id: "DL-001",
					area: "server",
					record: "../../server/other/design.md",
				},
			),
		);
		expect(got).toContainEqual({
			file: `${DECISION_DIR}/server/DL-001.md`,
			line: KEY_LINE.id,
			message: `DL-001: duplicate decision id (also defined in ${DECISION_DIR}/ui/DL-001.md)`,
		});
	});

	test("valid supersession target passes; missing target and self-supersession fail", () => {
		expect(
			evaluateCorpus(
				corpus(
					{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
					{ id: "DL-002" },
				),
			),
		).toEqual([]);
		const missing = evaluateCorpus(
			corpus({
				id: "DL-001",
				status: "Superseded by DL-999 (Matt, 2026-07-22)",
			}),
		);
		expect(missing).toContainEqual({
			file: `${DECISION_DIR}/ui/DL-001.md`,
			line: KEY_LINE.status,
			message: expect.stringContaining(
				"DL-001: Superseded by DL-999, which is not a decision file",
			),
		});
		const self = evaluateCorpus(
			corpus({
				id: "DL-001",
				status: "Superseded by DL-001 (Matt, 2026-07-22)",
			}),
		);
		expect(self).toHaveLength(1);
		expect(self[0]?.message).toContain("superseded by itself");
	});

	test("reports cycles once at their stable lowest-id decision locus", () => {
		const twoCycle = evaluateCorpus(
			corpus(
				{ id: "DL-002", status: "Superseded by DL-001 (Matt, 2026-07-22)" },
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
			),
		);
		const cycleViolations = twoCycle.filter((item) =>
			item.message.includes("supersession cycle"),
		);
		expect(cycleViolations).toHaveLength(1);
		expect(cycleViolations[0]).toMatchObject({
			file: `${DECISION_DIR}/ui/DL-001.md`,
			line: KEY_LINE.status,
		});
		const threeCycle = evaluateCorpus(
			corpus(
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
				{ id: "DL-002", status: "Superseded by DL-003 (Matt, 2026-07-22)" },
				{ id: "DL-003", status: "Superseded by DL-001 (Matt, 2026-07-22)" },
			),
		);
		expect(
			threeCycle.filter((item) => item.message.includes("supersession cycle")),
		).toHaveLength(1);
		const feedIn = evaluateCorpus(
			corpus(
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
				{ id: "DL-002", status: "Superseded by DL-003 (Matt, 2026-07-22)" },
				{ id: "DL-003", status: "Superseded by DL-004 (Matt, 2026-07-22)" },
				{ id: "DL-004", status: "Superseded by DL-003 (Matt, 2026-07-22)" },
			),
		);
		const loop = feedIn.filter((item) =>
			item.message.includes("supersession cycle"),
		);
		expect(loop).toHaveLength(1);
		expect(loop[0]?.message).toBe(
			"supersession cycle: DL-003 → DL-004 → DL-003",
		);
		const independent = evaluateCorpus(
			corpus(
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
				{ id: "DL-002", status: "Superseded by DL-001 (Matt, 2026-07-22)" },
				{ id: "DL-003", status: "Superseded by DL-004 (Matt, 2026-07-22)" },
				{ id: "DL-004", status: "Superseded by DL-003 (Matt, 2026-07-22)" },
			),
		);
		expect(
			independent.filter((item) => item.message.includes("supersession cycle")),
		).toHaveLength(2);
		const healthy = evaluateCorpus(
			corpus(
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
				{ id: "DL-002", status: "Superseded by DL-003 (Matt, 2026-07-22)" },
				{ id: "DL-003", status: "Active (Matt, 2026-07-22)" },
			),
		);
		expect(healthy).toEqual([]);
		const self = evaluateCorpus(
			corpus({
				id: "DL-001",
				status: "Superseded by DL-001 (Matt, 2026-07-22)",
			}),
		);
		expect(self).toHaveLength(1);
		expect(self[0]?.message).toContain("superseded by itself");
		expect(
			self.some((item) => item.message.includes("supersession cycle")),
		).toBe(false);
	});

	test("checks Record resolution, anchors, and large records", () => {
		const anchored = buildDecisionCorpus([
			{
				path: `${DECISION_DIR}/ui/DL-001.md`,
				text: decisionText({ record: "../../ui/record/design.md#present" }),
			},
		]);
		expect(
			evaluateCorpus(anchored, [], noChange, () => ({
				headings: ["present"],
				sizeBytes: 60_000,
			})),
		).toEqual([]);
		expect(
			evaluateCorpus(corpus(), [], noChange, () => ({
				headings: [],
				sizeBytes: 100,
			})),
		).toEqual([]);
		for (const fence of ["```", "~~~"]) {
			const fenced = recordContentFromText(`${fence}md\n# pseudo\n${fence}\n`);
			const deadAnchor = evaluateCorpus(
				corpus({ record: "../../ui/record/design.md#pseudo" }),
				[],
				noChange,
				() => fenced,
			);
			expect(
				deadAnchor.some((item) => item.message.includes("anchor not found")),
			).toBe(true);
		}
		const missingPath = evaluateCorpus(corpus(), [], noChange, () => null);
		expect(
			missingPath.some(
				(item) =>
					item.message.includes("does not resolve") &&
					item.line === KEY_LINE.record,
			),
		).toBe(true);
		const missingAnchor = evaluateCorpus(
			corpus({ record: "../../ui/record/design.md#missing" }),
			[],
			noChange,
			() => ({ headings: ["present"], sizeBytes: 100 }),
		);
		expect(
			missingAnchor.some((item) => item.message.includes("anchor not found")),
		).toBe(true);
		const largeWithoutAnchor = evaluateCorpus(corpus(), [], noChange, () => ({
			headings: [],
			sizeBytes: 50 * 1024 + 1,
		}));
		expect(
			largeWithoutAnchor.some((item) => item.message.includes("large record")),
		).toBe(true);
	});

	test("decision area must match a Record path under docs/designs", () => {
		const mismatch = evaluateCorpus(
			corpus({ record: "../../server/record/design.md" }),
			[],
			noChange,
			smallRecord,
		);
		expect(mismatch).toContainEqual({
			file: `${DECISION_DIR}/ui/DL-001.md`,
			line: KEY_LINE.record,
			message: expect.stringContaining("decision area must match"),
		});
		const outside = evaluateCorpus(
			corpus({ record: "../../../elsewhere/design.md" }),
			[],
			noChange,
			smallRecord,
		);
		expect(
			outside.some((item) => item.message.includes("decision area must match")),
		).toBe(true);
	});
	test("depth one permits a decision to cite a record on another surface", () => {
		const depthOne: LedgerConfig = {
			designsRoot: "docs/designs",
			surfaceDepth: 1,
			counter: { url: "https://example.invalid/counter", partition: "test" },
		};
		const decision = buildDecisionCorpus([
			{
				path: "docs/designs/alpha/decisions/ui/DL-001.md",
				text: decisionText({
					path: "docs/designs/alpha/decisions/ui/DL-001.md",
					record: "../../../beta/record/design.md",
				}),
			},
		]);
		expect(
			evaluateCorpus(decision, [], noChange, smallRecord, depthOne).some(
				(item) => item.message.includes("decision area must match"),
			),
		).toBe(false);
	});
});

describe("record Status headers", () => {
	test("bold key with the colon inside the bold parses", () => {
		expect(parseStatusValue("**Status:** Historical")).toEqual({
			kind: "Historical",
		});
		expect(parseStatusValue("> **Status:** Superseded by ../x.md")).toEqual({
			kind: "Superseded",
			path: "../x.md",
		});
	});

	test("duplicate headings resolve the same suffixed anchor in both checks", () => {
		expect(recordContentFromText("## X\n\n## X\n").headings).toEqual([
			"x",
			"x-1",
		]);
	});

	test("rejects prohibited Status and restricts Historical only when configured", () => {
		expect(
			evaluateCorpus(corpus(), [
				record(undefined, "# Title\n\nStatus: Draft\n"),
			]).some((item) => item.message.includes("malformed or prohibited")),
		).toBe(true);
		expect(
			evaluateCorpus(corpus(), [
				record(undefined, "# Title\n\nStatus: Historical\n"),
			]),
		).toEqual([]);
		expect(
			evaluateCorpus(
				corpus(),
				[record(undefined, "# Title\n\nStatus: Historical\n")],
				noChange,
				smallRecord,
				{ ...CONFIG, historicalChain: [] },
			).some((item) => item.message.includes("configured historical chain")),
		).toBe(true);
	});

	test("recordStatusScope changed checks PR-changed records only", () => {
		const draft = record(undefined, "# Title\n\nStatus: Draft\n");
		const scoped = { ...CONFIG, recordStatusScope: "changed" as const };
		const flagged = (changed: Changed) =>
			evaluateCorpus(corpus(), [draft], changed, smallRecord, scoped).some(
				(item) => item.message.includes("malformed or prohibited"),
			);
		expect(flagged(noChange)).toBe(false);
		expect(
			flagged({ files: [draft.path], body: "", headBranch: "feature" }),
		).toBe(true);
	});
	test("Status key spacing forms remain prohibited", () => {
		for (const header of ["**Status**: Draft", "Status : Draft"]) {
			const parsed = record(undefined, `# Title\n\n${header}\n`);
			expect(
				evaluateCorpus(corpus(), [parsed]).some((item) =>
					item.message.includes("malformed or prohibited"),
				),
			).toBe(true);
		}
	});

	test("checks record-level Superseded pointer and decision status agreement", () => {
		const statusHeader = record(
			"docs/designs/ui/record/design.md",
			"# Title\n\nStatus: Superseded by ../next/design.md\n",
		);
		expect(
			evaluateCorpus(corpus(), [statusHeader]).some((item) =>
				item.message.includes("disagrees with its decision status"),
			),
		).toBe(true);
		const matching = evaluateCorpus(
			corpus(
				{ id: "DL-001", status: "Superseded by DL-002 (Matt, 2026-07-22)" },
				{ id: "DL-002", record: "../../ui/next/design.md" },
			),
			[statusHeader],
		);
		expect(matching.some((item) => item.message.includes("disagrees"))).toBe(
			false,
		);
		const missing = evaluateCorpus(
			corpus(),
			[statusHeader],
			noChange,
			() => null,
		);
		expect(
			missing.some((item) =>
				item.message.includes("Status supersession does not resolve"),
			),
		).toBe(true);
		const nestedPointer = record(
			"docs/designs/ui/nested/record/design.md",
			"# Title\n\nStatus: Superseded by ui/next/design.md\n",
		);
		const nestedWrongBase = evaluateCorpus(
			corpus(),
			[nestedPointer],
			noChange,
			(path) =>
				path === "docs/designs/ui/ui/next/design.md" ? smallRecord() : null,
		);
		expect(
			nestedWrongBase.some((item) =>
				item.message.includes("Status supersession does not resolve"),
			),
		).toBe(true);
	});
});

test("branch exemptions do not skip record Status validation", () => {
	const got = evaluateCorpus(
		corpus(),
		[record(undefined, "# Title\n\nStatus: Draft\n")],
		{ files: [], body: null, headBranch: "renovate/update" },
	);
	expect(
		got.some((item) => item.message.includes("malformed or prohibited")),
	).toBe(true);
});

describe("decision completeness", () => {
	test("every governed root with records needs a valid decision in that area", () => {
		const got = evaluateCorpus(corpus(), [
			record("docs/designs/server/record/design.md"),
		]);
		expect(
			got.some(
				(item) =>
					item.message.includes("docs/designs/server/") &&
					item.message.includes("no valid decision file"),
			),
		).toBe(true);
	});
});

describe("touch coupling", () => {
	const changedRecord = "docs/designs/ui/record/design.md";
	test("record change requires a changed decision or Ledger-impact declaration", () => {
		const missing = evaluateCorpus(corpus(), [], {
			files: [changedRecord],
			body: "unrelated text",
			headBranch: "feature/change",
		});
		expect(
			missing.some((item) =>
				item.message.includes("changed decision file or Ledger-impact"),
			),
		).toBe(true);
	});

	test("a nested supporting record or a gamma record also couples", () => {
		for (const file of [
			"docs/designs/infra/runtime/sample/microvm-v3.md",
			"docs/designs/gamma/x/design.md",
		]) {
			const missing = evaluateCorpus(corpus(), [], {
				files: [file],
				body: "no declaration",
				headBranch: "feature/change",
			});
			expect(
				missing.some((item) =>
					item.message.includes("changed decision file or Ledger-impact"),
				),
			).toBe(true);
		}
	});

	test("any changed decision path or non-empty Ledger-impact satisfies coupling", () => {
		expect(
			evaluateCorpus(corpus(), [], {
				files: [changedRecord, `${DECISION_DIR}/server/DL-002.md`],
				body: null,
				headBranch: "feature/change",
			}),
		).toEqual([]);
		expect(
			evaluateCorpus(corpus(), [], {
				files: [changedRecord],
				body: "Ledger-impact: none",
				headBranch: "feature/change",
			}),
		).toEqual([]);
	});

	test.each(["> Ledger-impact: none", "LEDGER-IMPACT: x"])(
		"accepts declaration variant %s",
		(body) => {
			expect(
				evaluateCorpus(corpus(), [], {
					files: [changedRecord],
					body,
					headBranch: "feature/change",
				}),
			).toEqual([]);
		},
	);
	test("middle-of-name exempt prefix does not exempt coupling", () => {
		const violations = evaluateCorpus(corpus(), [], {
			files: [changedRecord],
			body: null,
			headBranch: "feature/renovate/x",
		});
		expect(
			violations.some((item) => item.message.includes("changed decision file")),
		).toBe(true);
	});
	test("empty changed set passes", () => {
		expect(evaluateCorpus(corpus(), [], noChange)).toEqual([]);
	});
	test.each(["renovate/update", "trunk-merge/pr-1/test"])(
		"branch exemption %s still skips coupling",
		(headBranch) => {
			expect(
				evaluateCorpus(corpus(), [], {
					files: [changedRecord],
					body: null,
					headBranch,
				}),
			).toEqual([]);
		},
	);
});

describe("runOnce", () => {
	const decisionPath = `${DECISION_DIR}/ui/DL-001.md`;
	const recordPath = "docs/designs/ui/record/design.md";

	function fixture(
		options: {
			paths?: string[];
			files?: Map<string, string>;
			changed?: Changed;
			list?: Deps["listDesignFiles"];
		} = {},
	) {
		const paths = options.paths ?? [decisionPath, recordPath];
		const files =
			options.files ??
			new Map([
				[decisionPath, decisionText()],
				[recordPath, "# Record\n\nBody\n"],
			]);
		const out: string[] = [];
		const errs: string[] = [];
		const deps: Deps = {
			root: "/fake",
			readText: async (_root, path) => files.get(path) ?? null,
			listDesignFiles: options.list ?? (async () => paths),
			readRecord: () => smallRecord(),
			readMergeBaseDecisionPaths: async () => [],
			readBaseDecisionPaths: async () => [],
			baseRef: "main",
			changed: options.changed ?? noChange,
			log: (message) => out.push(message),
			err: (message) => errs.push(message),
		};
		return { deps, out, errs };
	}

	test("valid fixture lists once and logs decision and record counts", async () => {
		let listingCalls = 0;
		const { deps, out } = fixture({
			list: async () => {
				listingCalls++;
				return [decisionPath, recordPath, `${DECISION_DIR}/README.md`];
			},
		});
		expect(await runOnce(deps, CONFIG)).toBe(0);
		expect(listingCalls).toBe(1);
		expect(out).toEqual([
			"design-ledger-gate: OK — 1 decision file(s), 1 record(s) status-checked; PR checks skipped (no PR context); citation checks off.",
		]);
	});

	test("changed Status scope skips records without PR context", async () => {
		const { deps, out } = fixture({
			files: new Map([
				[decisionPath, decisionText()],
				[recordPath, "# Record\n\nStatus: Draft\n"],
			]),
		});
		const config = { ...CONFIG, recordStatusScope: "changed" as const };
		expect(await runOnce(deps, config)).toBe(0);
		expect(out[0]).toContain("record Status skipped (no PR context)");
	});

	test("rejects a reintroduced DECISIONS.md", async () => {
		const { deps, errs } = fixture({
			paths: [decisionPath, recordPath, "docs/designs/DECISIONS.md"],
		});
		expect(await runOnce(deps, CONFIG)).toBe(1);
		expect(errs.join("\n")).toContain(
			"DECISIONS.md is retired: decision files belong in the configured decisions tree",
		);
	});

	test("reports misplaced decision-tree paths and DL files outside it", async () => {
		const { deps, errs } = fixture({
			paths: [
				decisionPath,
				recordPath,
				`${DECISION_DIR}/ui/extra.txt`,
				"docs/designs/server/DL-009.md",
			],
		});
		expect(await runOnce(deps, CONFIG)).toBe(1);
		expect(
			errs.filter((line) => line.includes("misplaced design file")),
		).toHaveLength(2);
	});

	test("zero valid decision files is a violation", async () => {
		const { deps, errs } = fixture({
			paths: [recordPath],
			files: new Map([[recordPath, "# Record\n"]]),
		});
		expect(await runOnce(deps, CONFIG)).toBe(1);
		expect(errs.join("\n")).toContain("no valid decision files were found");
	});

	test("malformed decision is reported at the parser line", async () => {
		const { deps, errs } = fixture({
			files: new Map([
				[decisionPath, decisionText({ status: "Draft" })],
				[recordPath, "# Record\n"],
			]),
		});
		expect(await runOnce(deps, CONFIG)).toBe(1);
		expect(errs.join("\n")).toContain(
			`${decisionPath}:4: malformed decision file`,
		);
	});

	test("checks decision conflict markers", async () => {
		const { deps, errs } = fixture({
			files: new Map([
				[
					decisionPath,
					`${decisionText()}\n<<<<<<< HEAD\n=======\n>>>>>>> theirs`,
				],
				[recordPath, "# Record\n"],
			]),
		});
		expect(await runOnce(deps, CONFIG)).toBe(1);
		expect(
			errs.filter((line) => line.includes("unresolved merge conflict marker")),
		).toHaveLength(3);
	});

	test("record Status checks still run and listing errors fail closed", async () => {
		const status = fixture({
			files: new Map([
				[decisionPath, decisionText()],
				[recordPath, "# Record\n\nStatus: Draft\n"],
			]),
		});
		expect(await runOnce(status.deps, CONFIG)).toBe(1);
		expect(status.errs.join("\n")).toContain("malformed or prohibited");
		const failed = fixture({
			list: async () => {
				throw new Error("boom");
			},
		});
		expect(await runOnce(failed.deps, CONFIG)).toBe(2);
		expect(failed.errs.join("\n")).toContain("boom");
	});
});

describe("conflictMarkerViolations", () => {
	test("flags git and jj markers, including lengthened runs", () => {
		const got = conflictMarkerViolations(
			"decision.md",
			[
				"<<<<<<< HEAD",
				"=======",
				">>>>>>> theirs",
				"%%%%%%%%%%% diff",
				"+++++++++++ side",
			].join("\n"),
		);
		expect(got.map((item) => item.line)).toEqual([1, 2, 3, 4, 5]);
	});

	test("ignores fenced examples and setext headings", () => {
		expect(
			conflictMarkerViolations(
				"record.md",
				["Heading", "==============", "```text", "<<<<<<< example", "```"].join(
					"\n",
				),
			),
		).toEqual([]);
	});
});

test("record content excludes fenced pseudo-headings from anchors", () => {
	const text = "# Real\n\n```text\n# Not real\n```\n";
	expect(recordContentFromText(text)).toEqual({
		headings: ["real"],
		sizeBytes: Buffer.byteLength(text, "utf8"),
	});
});

describe("prContextFrom", () => {
	test("valid PR coordinates enable the leg", () => {
		expect(
			prContextFrom({ REPO: "ExampleOrg/docs", PR_NUMBER: "1315" }),
		).toEqual({
			kind: "pr",
			repo: "ExampleOrg/docs",
			prNumber: "1315",
		});
	});

	test.each([
		[
			"missing coordinates on pull_request",
			{ GITHUB_EVENT_NAME: "pull_request" },
		],
		["missing repo", { PR_NUMBER: "1315" }],
		["invalid number", { REPO: "ExampleOrg/docs", PR_NUMBER: "abc" }],
		["zero", { REPO: "ExampleOrg/docs", PR_NUMBER: "0" }],
	])("%s fails closed", (_label, env) => {
		expect(prContextFrom(env).kind).toBe("error");
	});

	test.each([
		[
			"push with REPO and empty number",
			{ GITHUB_EVENT_NAME: "push", REPO: "ExampleOrg/docs", PR_NUMBER: "" },
		],
		["schedule", { GITHUB_EVENT_NAME: "schedule" }],
		["workflow dispatch", { GITHUB_EVENT_NAME: "workflow_dispatch" }],
		["local", {}],
	])("%s without PR coordinates skips", (_label, env) => {
		expect(prContextFrom(env)).toEqual({ kind: "skip" });
	});

	test("non-PR dispatch with valid PR coordinates runs the leg", () => {
		expect(
			prContextFrom({
				GITHUB_EVENT_NAME: "workflow_dispatch",
				REPO: "ExampleOrg/docs",
				PR_NUMBER: "1315",
			}),
		).toEqual({ kind: "pr", repo: "ExampleOrg/docs", prNumber: "1315" });
	});
	test("Woodpecker pull requests carry changed files, body, and branch", () => {
		expect(
			prContextFrom({
				CI_PIPELINE_EVENT: "pull_request",
				CI_PIPELINE_FILES: '["docs/designs/ui/record.md"]',
				CI_COMMIT_PULL_REQUEST_BODY: "Issue details",
				CI_COMMIT_SOURCE_BRANCH: "feature/design",
			}),
		).toEqual({
			kind: "woodpecker",
			changed: {
				files: ["docs/designs/ui/record.md"],
				body: "Issue details",
				headBranch: "feature/design",
			},
		});
	});

	test("non-PR Woodpecker events skip pull-request checks", () => {
		expect(
			prContextFrom({
				CI_PIPELINE_EVENT: "push",
				CI_PIPELINE_FILES: '["docs/designs/ui/record.md"]',
			}),
		).toEqual({ kind: "skip" });
	});
});

describe("CLI arguments", () => {
	const entry = new URL("./index.ts", import.meta.url).pathname;

	test("--help prints usage and exits successfully", () => {
		const result = spawnSync("bun", [entry, "--help"], { encoding: "utf8" });
		expect(result.status).toBe(0);
		expect(result.stdout).toContain(
			"Usage: design-ledger-gate --config <path>",
		);
	});

	test("missing arguments print usage and exit with code 2", () => {
		const result = spawnSync("bun", [entry], { encoding: "utf8" });
		expect(result.status).toBe(2);
		expect(result.stderr).toContain(
			"Usage: design-ledger-gate --config <path>",
		);
	});
});

function validConfig(): LedgerConfig {
	return {
		designsRoot: "docs/designs",
		surfaceDepth: 0,
		governedRoots: ["ui"],
		counter: { url: "https://example.invalid/counter", partition: "test" },
	};
}

function ledgerFixture(options: {
	config?: LedgerConfig;
	files: ReadonlyMap<string, string>;
	changed?: Changed;
	basePaths?: readonly string[];
	mergeBasePaths?: readonly string[];
}) {
	const config = options.config ?? validConfig();
	const errors: string[] = [];
	const logs: string[] = [];
	const paths = [...options.files.keys()];
	const deps: Deps = {
		root: "/repo",
		readText: async (_root, path) => options.files.get(path) ?? null,
		listDesignFiles: async () => paths,
		readRecord: (_root, path) => {
			const text = options.files.get(path);
			return text === undefined ? null : recordContentFromText(text);
		},
		readMergeBaseDecisionPaths: async () => options.mergeBasePaths ?? [],
		readBaseDecisionPaths: async () => options.basePaths ?? [],
		baseRef: "main",
		changed: options.changed ?? noChange,
		log: (message) => logs.push(message),
		err: (message) => errors.push(message),
	};
	return { config, deps, errors, logs };
}

function decisionAt(
	path: string,
	record: string,
	body = "# Decision\n\nChoose the stable option.\n",
): string {
	return [
		"---",
		`id: ${path.split("/").at(-1)?.replace(".md", "")}`,
		'decision: "Choose the stable option."',
		'status: "Active (Reviewer, 2026-01-01)"',
		`record: ${record}`,
		"---",
		body,
	].join("\n");
}

describe("loadLedgerConfig", () => {
	test.each([
		[
			"unknown key",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 0,
				governedRoots: ["ui"],
				counter: { url: "x", partition: "p" },
				unexpected: true,
			},
		],
		[
			"wrong type",
			{
				designsRoot: "docs/designs",
				surfaceDepth: "0",
				governedRoots: ["ui"],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"missing depth-zero roots",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 0,
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"unknown recordStatusScope",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				recordStatusScope: "some",
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"empty governed roots",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 0,
				governedRoots: [],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"empty exempt prefix",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				exemptBranchPrefixes: [""],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"non-array surfaces",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: 42,
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"empty surfaces",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: [],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"multiple depth-zero surfaces",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 0,
				governedRoots: ["ui"],
				surfaces: ["alpha", "beta"],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"duplicate surfaces",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: ["alpha", "alpha"],
				counter: { url: "x", partition: "p" },
			},
		],
		[
			"surface path",
			{
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: ["../alpha"],
				counter: { url: "x", partition: "p" },
			},
		],
	])("rejects %s", (_label, value) => {
		const path = join(temporaryDirectory(), "config.json");
		writeFileSync(path, JSON.stringify(value));
		expect(() => loadLedgerConfig(path)).toThrow();
	});
	test("defaults citationAmbiguousPaths to an empty list", () => {
		const path = join(temporaryDirectory(), "config.json");
		writeFileSync(
			path,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(loadLedgerConfig(path).citationAmbiguousPaths).toEqual([]);
	});
	test("accepts and returns citationAmbiguousPaths", () => {
		const path = join(temporaryDirectory(), "config.json");
		writeFileSync(
			path,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				citationAmbiguousPaths: ["shared/README.md"],
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(loadLedgerConfig(path).citationAmbiguousPaths).toEqual([
			"shared/README.md",
		]);
	});

	test("rejects citationAmbiguousPaths with a non-string entry", () => {
		const path = join(temporaryDirectory(), "config.json");
		writeFileSync(
			path,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				citationAmbiguousPaths: ["shared/README.md", 3],
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(() => loadLedgerConfig(path)).toThrow("citationAmbiguousPaths[1]");
	});
	test("rejects absolute citationAmbiguousPaths entries", () => {
		const path = join(temporaryDirectory(), "config.json");
		writeFileSync(
			path,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				citationAmbiguousPaths: ["../shared/README.md"],
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(() => loadLedgerConfig(path)).toThrow(
			"normalized repository-relative paths",
		);
	});
	test("accepts surfaces for depth zero and depth one layouts", () => {
		const depthZeroPath = join(temporaryDirectory(), "depth-zero.json");
		writeFileSync(
			depthZeroPath,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 0,
				governedRoots: ["ui"],
				surfaces: ["alpha"],
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(loadLedgerConfig(depthZeroPath).surfaces).toEqual(["alpha"]);
		const depthOnePath = join(temporaryDirectory(), "depth-one.json");
		writeFileSync(
			depthOnePath,
			JSON.stringify({
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: ["alpha", "beta"],
				counter: { url: "x", partition: "p" },
			}),
		);
		expect(loadLedgerConfig(depthOnePath).surfaces).toEqual(["alpha", "beta"]);
	});
	test("reports discovered depth-one surfaces outside the counter allowlist", async () => {
		const path = "docs/designs/gamma/record.md";
		const fixture = ledgerFixture({
			config: {
				designsRoot: "docs/designs",
				surfaceDepth: 1,
				surfaces: ["alpha"],
				counter: { url: "https://example.invalid", partition: "test" },
			},
			files: new Map([[path, "# Unknown surface\n"]]),
		});
		expect(await runOnce(fixture.deps, fixture.config)).toBe(1);
		expect(fixture.errors.join("\n")).toContain(
			"unknown design surface: gamma",
		);
	});
});

describe("depth-one layout", () => {
	const depthOne: LedgerConfig = {
		designsRoot: "docs/designs",
		surfaceDepth: 1,
		counter: { url: "https://example.invalid/counter", partition: "test" },
	};
	test("classifies surface decisions and requires a decision for every surface with records", () => {
		const firstDecision = "docs/designs/alpha/decisions/ui/DL-001.md";
		const alphaRecord = "docs/designs/alpha/ui/record.md";
		const betaRecord = "docs/designs/beta/ui/record.md";
		expect(classifyDesignPath(firstDecision, depthOne)).toBe("decision");
		const corpus = buildDecisionCorpus([
			{
				path: firstDecision,
				text: decisionAt(firstDecision, "../../../ui/record.md"),
			},
		]);
		const failures = evaluate(
			corpus,
			[record(alphaRecord), record(betaRecord)],
			noChange,
			() => smallRecord(),
			depthOne,
		);
		expect(
			failures.some((item) => item.file === "docs/designs/beta/decisions"),
		).toBe(true);
		expect(
			failures.some((item) => item.file === "docs/designs/alpha/decisions"),
		).toBe(false);
	});
});

describe("optional validation legs", () => {
	test("citations reject past-EOF lines and accept valid references", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const sourcePath = "docs/designs/ui/source.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\nSee `../source.md:3` here.\n"],
			[sourcePath, "# Source\n"],
		]);
		const bad = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(bad.deps, bad.config)).toBe(1);
		expect(bad.errors.join("\n")).toContain("exceeds target line count");
		files.set(recordPath, "# Record\nSee `../source.md:1` here.\n");
		const good = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(good.deps, good.config)).toBe(0);
		expect(good.logs.join("\n")).toContain(
			"1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)",
		);
	});
	test("citations parse inline prose and validate every comma-separated part", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const sourcePath = "docs/designs/ui/source.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "See note `../source.md:1,2` in this paragraph.\n"],
			[sourcePath, "# Source\nline two\n"],
		]);
		const good = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(good.deps, good.config)).toBe(0);
		expect(good.logs.join("\n")).toContain(
			"2/2 citation(s) (0 unresolvable, 0 repo-ambiguous)",
		);
		files.set(recordPath, "See note `../source.md:1,3` in this paragraph.\n");
		const bad = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(bad.deps, bad.config)).toBe(1);
		expect(bad.errors.join("\n")).toContain(
			"source.md:3 exceeds target line count 2",
		);
		files.set(recordPath, "Note `../source.md:0,2-1,2`.\n");
		const skipped = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(skipped.deps, skipped.config)).toBe(0);
		expect(skipped.logs.join("\n")).toContain(
			"1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)",
		);
	});

	test("bare citation filenames do not fall back to the repository root", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "See `README.md:1` in this record.\n"],
			["README.md", "# Root README\n"],
		]);
		const fixture = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(fixture.deps, fixture.config)).toBe(0);
		expect(fixture.logs.join("\n")).toContain(
			"0/1 citation(s) (1 unresolvable, 0 repo-ambiguous)",
		);
	});

	test("configured root citation paths are refused as repo-ambiguous", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "See `shared/README.md:1` in this record.\n"],
			["shared/README.md", "# Shared README\n"],
		]);
		const fixture = ledgerFixture({
			config: {
				...validConfig(),
				citationAmbiguousPaths: ["shared/README.md"],
				legs: { citations: true },
			},
			files,
		});
		expect(await runOnce(fixture.deps, fixture.config)).toBe(0);
		expect(fixture.logs.join("\n")).toContain(
			"0/1 citation(s) (0 unresolvable, 1 repo-ambiguous)",
		);
	});

	test("citations ignore nested-looking fences but reject an unclosed fence", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const sourcePath = "docs/designs/ui/source.md";
		const fenced = [
			"# Record",
			"````md",
			"```",
			"Citation `../ui/source.md:99` stays fenced.",
			"```",
			"````",
			"Valid inline `../source.md:1` outside the fence.",
			"",
		].join("\n");
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, fenced],
			[sourcePath, "# Source\n"],
		]);
		const good = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(good.deps, good.config)).toBe(0);
		expect(good.logs.join("\n")).toContain(
			"1/1 citation(s) (0 unresolvable, 0 repo-ambiguous)",
		);
		files.set(recordPath, "# Record\n````md\n`../source.md:99`\n");
		const unclosed = ledgerFixture({
			config: { ...validConfig(), legs: { citations: true } },
			files,
		});
		expect(await runOnce(unclosed.deps, unclosed.config)).toBe(1);
		expect(unclosed.errors.join("\n")).toContain("unclosed Markdown fence");
	});
	test("enabled prose legs report one unclosed fence per file", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\n````md\nunfinished `../source.md:99`\n"],
		]);
		const result = ledgerFixture({
			config: {
				...validConfig(),
				legs: { citations: true, recordLinks: true, errata: true },
			},
			files,
		});
		expect(await runOnce(result.deps, result.config)).toBe(1);
		expect(
			result.errors.filter((line) => line.includes("unclosed Markdown fence")),
		).toHaveLength(1);
	});

	test("recordLinks reject dead anchors and accept heading slugs outside fences", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const sourcePath = "docs/designs/ui/source.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\n[link](../source.md#missing)\n"],
			[sourcePath, "# Real Heading\n```md\n# Hidden\n```\n"],
		]);
		const bad = ledgerFixture({
			config: { ...validConfig(), legs: { recordLinks: true } },
			files,
		});
		expect(await runOnce(bad.deps, bad.config)).toBe(1);
		expect(bad.errors.join("\n")).toContain("anchor not found");
		files.set(recordPath, "# Record\n[link](../source.md#real-heading)\n");
		const good = ledgerFixture({
			config: { ...validConfig(), legs: { recordLinks: true } },
			files,
		});
		expect(await runOnce(good.deps, good.config)).toBe(0);
	});

	test("recordLinks reject paths escaping the repo root", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\n[link](../../../../../outside.md#anchor)\n"],
		]);
		const result = ledgerFixture({
			config: { ...validConfig(), legs: { recordLinks: true } },
			files,
		});
		expect(await runOnce(result.deps, result.config)).toBe(1);
		expect(result.errors.join("\n")).toContain("escapes the repository");
	});
	test("recordLinks reject paths escaping without an anchor", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\n[link](../../../../../outside.md)\n"],
		]);
		const result = ledgerFixture({
			config: { ...validConfig(), legs: { recordLinks: true } },
			files,
		});
		expect(await runOnce(result.deps, result.config)).toBe(1);
		expect(result.errors.join("\n")).toContain("escapes the repository");
	});

	test("errata rejects stale quotes and accepts ordered exact quotes", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const make = (quote: string) =>
			[
				"---",
				"id: DL-001",
				'decision: "Choose the stable option."',
				'status: "Active (Reviewer, 2026-01-01)"',
				"record: ../../ui/record/design.md",
				"---",
				"# Decision",
				"Errata: E1",
				"",
				"The earlier wording was wrong.",
				"",
				"## Errata",
				"### E1 — 2026-01-02 (Reviewer)",
				`Correction of "${quote}".`,
				"",
			].join("\n");
		const files = new Map([
			[decisionPath, make("gone text")],
			[recordPath, "# Record\n"],
		]);
		const bad = ledgerFixture({
			config: { ...validConfig(), legs: { errata: true } },
			files,
		});
		expect(await runOnce(bad.deps, bad.config)).toBe(1);
		expect(bad.errors.join("\n")).toContain(
			"no longer appears above the section",
		);
		files.set(decisionPath, make("The earlier wording was wrong."));
		const good = ledgerFixture({
			config: { ...validConfig(), legs: { errata: true } },
			files,
		});
		expect(await runOnce(good.deps, good.config)).toBe(0);
	});
	test("errata validates headings, ordering, final H2, and marker IDs", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const text = [
			"---",
			"id: DL-001",
			'decision: "Choose the stable option."',
			'status: "Active (Reviewer, 2026-01-01)"',
			"record: ../../ui/record/design.md",
			"---",
			"# Decision",
			"Errata: E2",
			'Prior wording includes "old text".',
			"",
			"## Errata",
			"### E1 — 2026-01-02 (Reviewer)",
			'Correction of "old text".',
			"",
		].join("\n");
		const fixture = ledgerFixture({
			config: { ...validConfig(), legs: { errata: true } },
			files: new Map([
				[decisionPath, text],
				[recordPath, "# Record\n"],
			]),
		});
		expect(await runOnce(fixture.deps, fixture.config)).toBe(1);
		expect(fixture.errors.join("\n")).toContain("marker IDs must match");
	});
	test("errata rejects malformed headings and a non-final Errata H2", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const text = [
			"---",
			"id: DL-001",
			'decision: "Choose the stable option."',
			'status: "Active (Reviewer, 2026-01-01)"',
			"record: ../../ui/record/design.md",
			"---",
			"# Decision",
			"Errata: E1",
			'Prior wording includes "old text".',
			"",
			"## Errata",
			"### E1 malformed",
			'Correction of "old text".',
			"",
			"## More",
		].join("\n");
		const fixture = ledgerFixture({
			config: { ...validConfig(), legs: { errata: true } },
			files: new Map([
				[decisionPath, text],
				[recordPath, "# Record\n"],
			]),
		});
		expect(await runOnce(fixture.deps, fixture.config)).toBe(1);
		expect(fixture.errors.join("\n")).toContain("must be the final H2");
		expect(fixture.errors.join("\n")).toContain(
			"malformed errata entry heading",
		);
	});
	test.each([
		[
			"missing Errata marker",
			[
				"# Decision",
				'Prior wording includes "old text".',
				"## Errata",
				"### E1 — 2026-01-02 (Reviewer)",
				'Correction of "old text".',
			],
			"Errata section requires an Errata marker line",
		],
		[
			"marker without section",
			["# Decision", "Errata: E1", 'Prior wording includes "old text".'],
			"Errata marker requires an Errata section",
		],
		[
			"missing numbered entry",
			[
				"# Decision",
				"Errata: E2",
				'Prior wording includes "old text".',
				"## Errata",
				"### E2 — 2026-01-02 (Reviewer)",
				'Correction of "old text".',
			],
			"errata IDs must be numbered E1..En in order",
		],
		[
			"entry without quote",
			[
				"# Decision",
				"Errata: E1",
				'Prior wording includes "old text".',
				"## Errata",
				"### E1 — 2026-01-02 (Reviewer)",
				"Correction without a quote.",
			],
			"errata entry requires a quoted wrong text",
		],
	])(
		"reports the expected errata diagnostic for %s",
		async (_name, lines, message) => {
			const decisionPath = "docs/designs/decisions/ui/DL-001.md";
			const recordPath = "docs/designs/ui/record/design.md";
			const files = new Map([
				[
					decisionPath,
					decisionAt(
						decisionPath,
						"../../ui/record/design.md",
						lines.join("\n"),
					),
				],
				[recordPath, "# Record\n"],
			]);
			const result = ledgerFixture({
				config: { ...validConfig(), legs: { errata: true } },
				files,
			});
			expect(await runOnce(result.deps, result.config)).toBe(1);
			expect(result.errors.join("\n")).toContain(message);
		},
	);

	test("mainIds compare merge-base additions against base-tip IDs", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[recordPath, "# Record\n"],
		]);
		const changed: Changed = {
			files: [decisionPath],
			body: "",
			headBranch: "feature/test",
		};
		const duplicate = ledgerFixture({
			config: { ...validConfig(), legs: { mainIds: true } },
			files,
			changed,
			mergeBasePaths: [],
			basePaths: ["docs/designs/decisions/server/DL-001.md"],
		});
		expect(await runOnce(duplicate.deps, duplicate.config)).toBe(1);
		expect(duplicate.errors.join("\n")).toContain(
			"already exists on base branch tip",
		);
		const oldId = ledgerFixture({
			config: { ...validConfig(), legs: { mainIds: true } },
			files,
			changed,
			mergeBasePaths: ["docs/designs/decisions/server/DL-001.md"],
			basePaths: ["docs/designs/decisions/server/DL-001.md"],
		});
		expect(await runOnce(oldId.deps, oldId.config)).toBe(0);
		const unusedId = ledgerFixture({
			config: { ...validConfig(), legs: { mainIds: true } },
			files,
			changed,
			mergeBasePaths: [],
			basePaths: ["docs/designs/decisions/server/DL-500.md"],
		});
		expect(await runOnce(unusedId.deps, unusedId.config)).toBe(0);
	});
	test.each(["merge-base", "base-tip"])(
		"mainIds fail closed when %s listing fails",
		async (failedListing) => {
			const decisionPath = "docs/designs/decisions/ui/DL-001.md";
			const recordPath = "docs/designs/ui/record/design.md";
			const files = new Map([
				[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
				[recordPath, "# Record\n"],
			]);
			const fixture = ledgerFixture({
				config: { ...validConfig(), legs: { mainIds: true } },
				files,
				changed: {
					files: [decisionPath],
					body: "",
					headBranch: "feature/test",
				},
			});
			const brokenDeps = {
				...fixture.deps,
				readMergeBaseDecisionPaths: async () => {
					if (failedListing === "merge-base")
						throw new Error("merge-base failed");
					return [];
				},
				readBaseDecisionPaths: async () => {
					if (failedListing === "base-tip") throw new Error("base-tip failed");
					return [];
				},
			};
			expect(await runOnce(brokenDeps, fixture.config)).toBe(2);
			expect(fixture.errors.join("\n")).toContain(
				"cannot read base decision listing",
			);
		},
	);

	test("all extra legs stay off by default", async () => {
		const decisionPath = "docs/designs/decisions/ui/DL-001.md";
		const recordPath = "docs/designs/ui/record/design.md";
		const sourcePath = "docs/designs/ui/source.md";
		const files = new Map([
			[decisionPath, decisionAt(decisionPath, "../../ui/record/design.md")],
			[
				recordPath,
				"# Record\n`../source.md:99`\n[link](../source.md#missing)\n## Errata\n### E1 malformed\n",
			],
			[sourcePath, "# Source\n"],
		]);
		const result = ledgerFixture({
			files,
			changed: { files: [decisionPath], body: "", headBranch: "feature/test" },
			mergeBasePaths: [],
			basePaths: ["docs/designs/decisions/server/DL-001.md"],
		});
		expect(await runOnce(result.deps, result.config)).toBe(0);
		const failures = await Promise.all(
			[
				{ citations: true },
				{ errata: true },
				{ recordLinks: true },
				{ mainIds: true },
			].map((leg) => {
				const enabled = ledgerFixture({
					files,
					changed: result.deps.changed,
					mergeBasePaths: [],
					basePaths: ["docs/designs/decisions/server/DL-001.md"],
				});
				return runOnce(enabled.deps, { ...enabled.config, legs: leg });
			}),
		);
		expect(failures).toEqual([1, 1, 1, 1]);
	});
});
