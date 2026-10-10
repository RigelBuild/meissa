import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import type { LedgerConfig } from "@rigelbuild/design-ledger-gate";
import {
	assertReconcilableLedgers,
	formatReconcileOutput,
	type ReconcileResponse,
	reconcile,
	runOnce,
} from "./index.ts";

const depthZero: LedgerConfig = {
	designsRoot: "docs/designs",
	surfaceDepth: 0,
	governedRoots: ["ui"],
	surfaces: ["alpha"],
	counter: { url: "https://counter.example.test///", partition: "public-docs" },
};
const depthOne: LedgerConfig = {
	...depthZero,
	surfaceDepth: 1,
	governedRoots: undefined,
	surfaces: ["alpha", "beta"],
};

function decisionFile(id: string): string {
	return [
		"---",
		`id: ${id}`,
		`decision: "Choose ${id}"`,
		'status: "Active (Reviewer, 2026-01-01)"',
		"record: ../record.md",
		"---",
		"Decision body.",
	].join("\n");
}

function response(
	stale: ReconcileResponse["stale"] = [],
	duplicates: ReconcileResponse["duplicates"] = [],
): Response {
	return Response.json({ updated: 1, inserted: 2, stale, duplicates });
}

const temporaryDirectories: string[] = [];
afterEach(() => {
	for (const path of temporaryDirectories.splice(0))
		rmSync(path, { recursive: true, force: true });
});

function fixtureRoot(
	config: LedgerConfig,
	files: ReadonlyMap<string, string>,
): {
	root: string;
	configPath: string;
} {
	const root = mkdtempSync(join(tmpdir(), "dl-reconcile-test-"));
	temporaryDirectories.push(root);
	for (const [path, text] of files) {
		const absolute = join(root, path);
		mkdirSync(dirname(absolute), { recursive: true });
		writeFileSync(absolute, text);
	}
	const configPath = join(root, "config.json");
	writeFileSync(configPath, JSON.stringify(config));
	return { root, configPath };
}

describe("assertReconcilableLedgers", () => {
	test("assigns the configured surface at depth zero", () => {
		const path = "docs/designs/decisions/ui/DL-002.md";
		expect(
			assertReconcilableLedgers(
				depthZero,
				new Map([[path, decisionFile("DL-002")]]),
			),
		).toEqual({
			repo: "public-docs",
			landed: [{ id: "DL-002", surface: "alpha", ref: "none" }],
		});
	});

	test("takes the depth-one surface from the path and keeps duplicate IDs", () => {
		const first = "docs/designs/alpha/decisions/ui/DL-002.md";
		const second = "docs/designs/beta/decisions/server/DL-002.md";
		expect(
			assertReconcilableLedgers(
				depthOne,
				new Map([
					[first, decisionFile("DL-002")],
					[second, decisionFile("DL-002")],
				]),
			),
		).toEqual({
			repo: "public-docs",
			landed: [
				{ id: "DL-002", surface: "alpha", ref: "none" },
				{ id: "DL-002", surface: "beta", ref: "none" },
			],
		});
	});

	test("refuses unknown surfaces, empty discovery, and malformed files", () => {
		const unknown = "docs/designs/gamma/decisions/ui/DL-001.md";
		expect(() =>
			assertReconcilableLedgers(
				depthOne,
				new Map([[unknown, decisionFile("DL-001")]]),
			),
		).toThrow("unknown design surface");
		expect(() => assertReconcilableLedgers(depthOne, new Map())).toThrow(
			"refusing to post an empty frontier",
		);
		const malformed = "docs/designs/alpha/decisions/ui/DL-001.md";
		expect(() =>
			assertReconcilableLedgers(
				depthOne,
				new Map([[malformed, "not front matter"]]),
			),
		).toThrow(`${malformed}: line 1`);
	});
});

describe("reconcile service", () => {
	test("posts to the configured counter and parses stale and duplicate results", async () => {
		const path = "docs/designs/decisions/ui/DL-001.md";
		const body = assertReconcilableLedgers(
			depthZero,
			new Map([[path, decisionFile("DL-001")]]),
		);
		const stale = [
			{
				id: "DL-010",
				surface: "alpha",
				ref: "RIG-2",
				lane: "branch",
				date: "2026-09-01",
			},
		];
		const duplicates = [{ id: "DL-010", surfaces: ["alpha", "beta"] }];
		let request: Request | undefined;
		const result = await reconcile(body, depthZero, " token ", {
			fetchFn: async (input, init) => {
				request = new Request(String(input), init);
				return response(stale, duplicates);
			},
		});
		expect(request?.url).toBe("https://counter.example.test/reconcile");
		expect(request?.headers.get("Authorization")).toBe("Bearer token");
		expect(await request?.json()).toEqual(body);
		expect(result.stale).toEqual(stale);
		expect(result.duplicates).toEqual(duplicates);
		expect(formatReconcileOutput(result).split("\n")).toEqual([
			"updated: 1  inserted: 2",
			"stale: 1",
			"  DL-010 alpha branch RIG-2 2026-09-01",
			"duplicates: 1",
			"  DL-010 alpha beta",
		]);
	});

	test("rejects blank credentials and invalid service responses", async () => {
		let called = false;
		await expect(
			reconcile({ repo: "public-docs", landed: [] }, depthZero, " ", {
				fetchFn: async () => {
					called = true;
					return response();
				},
			}),
		).rejects.toThrow("DL_CLAIM_TOKEN is required");
		expect(called).toBe(false);
		await expect(
			reconcile({ repo: "public-docs", landed: [] }, depthZero, "token", {
				fetchFn: async () => Response.json({ updated: 1 }),
			}),
		).rejects.toThrow("invalid reconciliation response");
	});
	describe("reconcile CLI", () => {
		test("check mode performs no request and reports discovered count", async () => {
			const path = "docs/designs/decisions/ui/DL-001.md";
			const fixture = fixtureRoot(
				depthZero,
				new Map([[path, decisionFile("DL-001")]]),
			);
			let posted = false;
			const output: string[] = [];
			expect(
				await runOnce(["--config", fixture.configPath, "--check"], {
					root: fixture.root,
					fetchFn: async () => {
						posted = true;
						return response();
					},
					log: (message) => output.push(message),
					err: (message) => output.push(message),
				}),
			).toBe(0);
			expect(posted).toBe(false);
			expect(output).toEqual([
				"Design ledger parse check passed (1 decision files).",
			]);
		});

		test("returns usage errors for empty discovery and malformed files without posting", async () => {
			for (const files of [
				new Map<string, string>(),
				new Map([["docs/designs/decisions/ui/DL-001.md", "broken"]]),
			]) {
				const fixture = fixtureRoot(depthZero, files);
				let posted = false;
				const errors: string[] = [];
				expect(
					await runOnce(["--config", fixture.configPath], {
						root: fixture.root,
						token: "token",
						fetchFn: async () => {
							posted = true;
							return response();
						},
						err: (message) => errors.push(message),
					}),
				).toBe(2);
				expect(posted).toBe(false);
				expect(errors).toHaveLength(1);
			}
		});

		test("stale-exit returns one when stale claims remain", async () => {
			const path = "docs/designs/decisions/ui/DL-001.md";
			const fixture = fixtureRoot(
				depthZero,
				new Map([[path, decisionFile("DL-001")]]),
			);
			const logs: string[] = [];
			expect(
				await runOnce(["--config", fixture.configPath, "--stale-exit"], {
					root: fixture.root,
					token: "token",
					fetchFn: async () =>
						response([
							{
								id: "DL-007",
								surface: "alpha",
								ref: "none",
								lane: "branch",
								date: "2026-09-01",
							},
						]),
					log: (message) => logs.push(message),
				}),
			).toBe(1);
			expect(logs[0]).toContain("updated: 1  inserted: 2");
			expect(logs[0]).toContain("DL-007 alpha branch none 2026-09-01");
		});
	});
});
