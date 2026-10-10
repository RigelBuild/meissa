import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { LedgerConfig } from "@rigelbuild/design-ledger-gate";
import {
	readDlClaimToken,
	validateDlClaimToken,
} from "@rigelbuild/design-ledger-gate";
import {
	buildClaimBody,
	claim,
	formatClaimed,
	parseArgs,
	runOnce,
} from "./index.ts";

const oneSurface: LedgerConfig = {
	designsRoot: "docs/designs",
	surfaceDepth: 0,
	governedRoots: ["ui"],
	surfaces: ["alpha"],
	counter: { url: "https://counter.example.test/", partition: "public-docs" },
};
const severalSurfaces: LedgerConfig = {
	...oneSurface,
	surfaceDepth: 1,
	governedRoots: undefined,
	surfaces: ["alpha", "beta"],
};
const claimArgs = { ref: "RIG-42", lane: "feature/decision", count: 2 };
const body = buildClaimBody(oneSurface, { ...claimArgs, count: 1 });
const claimed = [{ id: "DL-377", date: "2026-09-29" }];
const temporaryDirectories: string[] = [];
afterEach(() => {
	for (const path of temporaryDirectories.splice(0))
		rmSync(path, { recursive: true, force: true });
});

describe("claim arguments and request", () => {
	test("defaults to the only configured surface", () => {
		expect(parseArgs(["--ref=none", "--lane=feature/b"], oneSurface)).toEqual({
			ref: "none",
			lane: "feature/b",
			count: 1,
			surface: "alpha",
		});
		expect(buildClaimBody(oneSurface, claimArgs)).toEqual({
			repo: "public-docs",
			surface: "alpha",
			...claimArgs,
		});
	});

	test("requires a known explicit surface for a multi-surface config", () => {
		expect(() =>
			parseArgs(["--ref", "none", "--lane", "x"], severalSurfaces),
		).toThrow("--surface is required");
		expect(() =>
			parseArgs(
				["--ref", "none", "--lane", "x", "--surface", "unknown"],
				severalSurfaces,
			),
		).toThrow("Unknown surface");
		expect(
			parseArgs(
				["--ref", "none", "--lane", "x", "--surface", "beta"],
				severalSurfaces,
			),
		).toMatchObject({ surface: "beta" });
	});
	test("requires an explicit surface when building a multi-surface body", () => {
		expect(() => buildClaimBody(severalSurfaces, claimArgs)).toThrow(
			"valid surface from config.surfaces",
		);
	});

	test("validates refs, lane, counts, and unknown flags", () => {
		for (const argv of [
			["--lane", "x"],
			["--ref", "rig-12", "--lane", "x"],
			["--ref", "none", "--lane", "  "],
			["--ref", "none", "--lane", "x", "--count", "0"],
			["--ref", "none", "--lane", "x", "--count", "11"],
			["--ref", "none", "--lane", "x", "--wat"],
		])
			expect(() => parseArgs(argv, oneSurface)).toThrow();
	});

	test("accepts separated and equals values with an explicit surface", () => {
		expect(
			parseArgs(
				["--ref", "RIG-42", "--lane", "feature/a", "--count", "2"],
				oneSurface,
			),
		).toEqual({ ref: "RIG-42", lane: "feature/a", count: 2, surface: "alpha" });
		expect(
			parseArgs(
				["--ref=none", "--lane=feature/b", "--surface=beta"],
				severalSurfaces,
			),
		).toEqual({ ref: "none", lane: "feature/b", count: 1, surface: "beta" });
	});
});

describe("claim service", () => {
	test("posts the configured URL and partition with a trimmed bearer token", async () => {
		let request: Request | undefined;
		await claim(body, " token ", oneSurface, {
			fetchFn: async (input, init) => {
				request = new Request(String(input), init);
				return Response.json({ ids: claimed, extra: true });
			},
		});
		expect(request?.url).toBe("https://counter.example.test/claim");
		expect(request?.headers.get("Authorization")).toBe("Bearer token");
		expect(await request?.json()).toEqual(body);
	});

	test("rejects blank credentials before posting", async () => {
		let called = false;
		await expect(
			claim(body, " ", oneSurface, {
				fetchFn: async () => {
					called = true;
					return Response.json({ ids: claimed });
				},
			}),
		).rejects.toThrow("DL_CLAIM_TOKEN is required");
		expect(called).toBe(false);
	});

	test("includes the configured service URL in an uncertain failure", async () => {
		await expect(
			claim(body, "token", oneSurface, {
				fetchFn: async () => new Response("failure", { status: 502 }),
			}),
		).rejects.toThrow("https://counter.example.test/status?repo=public-docs");
	});
	test("classifies service failures without hiding uncertain consumed IDs", async () => {
		const failureMessage = async (status: number, payload: unknown) => {
			try {
				await claim(body, "token", oneSurface, {
					fetchFn: async () => Response.json(payload, { status }),
				});
				return "";
			} catch (error) {
				return error instanceof Error ? error.message : String(error);
			}
		};
		expect(await failureMessage(400, { error: "invalid" })).not.toContain(
			"may already be consumed",
		);
		expect(await failureMessage(401, { error: "unauthorized" })).toContain(
			"Token missing or wrong",
		);
		expect(await failureMessage(429, { error: "limited" })).toContain(
			"Rate limited",
		);
		expect(await failureMessage(503, { error: "not_hydrated" })).toContain(
			"has not hydrated",
		);
		expect(await failureMessage(502, {})).toContain("may already be consumed");
	});

	test("uses the requested timeout deadline for its fetch", async () => {
		const controller = new AbortController();
		let requestedTimeout = 0;
		let receivedSignal: AbortSignal | null | undefined;
		await claim(body, "token", oneSurface, {
			timeoutMs: 12_345,
			timeoutSignal: (timeoutMs) => {
				requestedTimeout = timeoutMs;
				return controller.signal;
			},
			fetchFn: async (_input, init) => {
				receivedSignal = init?.signal;
				return Response.json({ ids: claimed });
			},
		});
		expect(requestedTimeout).toBe(12_345);
		expect(receivedSignal).toBe(controller.signal);
		controller.abort();
	});

	test("rejects malformed and wrong-count responses", async () => {
		await expect(
			claim(body, "token", oneSurface, {
				fetchFn: async () => Response.json({ ids: [] }),
			}),
		).rejects.toThrow("requested number of ids");
		await expect(
			claim(body, "token", oneSurface, {
				fetchFn: async () =>
					Response.json({ ids: [{ id: "DL-37", date: "bad" }] }),
			}),
		).rejects.toThrow("malformed id");
	});
	test("warns about possible consumption only after the counter may advance", async () => {
		const failureMessage = async (fetchFn: () => Promise<Response>) => {
			try {
				await claim(body, "token", oneSurface, { fetchFn });
				return "";
			} catch (error) {
				return error instanceof Error ? error.message : String(error);
			}
		};
		expect(
			await failureMessage(async () =>
				Response.json({ ids: [claimed[0], { id: "DL-378", date: "bad" }] }),
			),
		).toContain("Returned: DL-377, DL-378.");
		expect(await failureMessage(async () => new Response("bad"))).toContain(
			"may already be consumed",
		);
		expect(
			await failureMessage(async () =>
				Response.json({ error: "invalid" }, { status: 400 }),
			),
		).not.toContain("may already be consumed");
	});
});

describe("claim CLI", () => {
	test("returns usage errors before posting when surface selection is invalid", async () => {
		const directory = mkdtempSync(join(tmpdir(), "dl-claim-cli-test-"));
		temporaryDirectories.push(directory);
		const configPath = join(directory, "config.json");
		writeFileSync(configPath, JSON.stringify(severalSurfaces));
		let called = false;
		const errors: string[] = [];
		const result = await runOnce(
			["--config", configPath, "--ref", "none", "--lane", "feature/b"],
			{
				token: "token",
				fetchFn: async () => {
					called = true;
					return Response.json({ ids: claimed });
				},
				err: (message) => errors.push(message),
			},
		);
		expect(result).toBe(2);
		expect(called).toBe(false);
		expect(errors[0]).toContain("--surface is required");
	});

	test("posts a valid configured claim and reports the returned ID", async () => {
		const directory = mkdtempSync(join(tmpdir(), "dl-claim-cli-test-"));
		temporaryDirectories.push(directory);
		const configPath = join(directory, "config.json");
		writeFileSync(configPath, JSON.stringify(oneSurface));
		const output: string[] = [];
		const result = await runOnce(
			["--config", configPath, "--ref", "none", "--lane", "feature/b"],
			{
				token: "token",
				fetchFn: async () => Response.json({ ids: claimed }),
				log: (message) => output.push(message),
			},
		);
		expect(result).toBe(0);
		expect(output).toEqual(["DL-377 (claimed 2026-09-29)"]);
	});
	test("request rejection prints endpoint and error name only", async () => {
		const directory = mkdtempSync(join(tmpdir(), "dl-claim-cli-test-"));
		temporaryDirectories.push(directory);
		const configPath = join(directory, "config.json");
		writeFileSync(configPath, JSON.stringify(oneSurface));
		const output: string[] = [];
		expect(
			await runOnce(
				["--config", configPath, "--ref", "none", "--lane", "feature/b"],
				{
					token: "token",
					fetchFn: async () => {
						throw new Error("Authorization: Bearer secret");
					},
					err: (message) => output.push(message),
				},
			),
		).toBe(1);
		expect(output.join("\n")).toContain(
			"request to https://counter.example.test/claim failed (Error)",
		);
		expect(output.join("\n")).not.toContain("Bearer secret");
		expect(output.join("\n")).toContain("do not rerun");
	});

	test("service errors keep the counter's message and the do-not-rerun warning", async () => {
		const directory = mkdtempSync(join(tmpdir(), "dl-claim-cli-test-"));
		temporaryDirectories.push(directory);
		const configPath = join(directory, "config.json");
		writeFileSync(configPath, JSON.stringify(oneSurface));
		const output: string[] = [];
		expect(
			await runOnce(
				["--config", configPath, "--ref", "none", "--lane", "feature/b"],
				{
					token: "token",
					fetchFn: async () => new Response("bad gateway", { status: 502 }),
					err: (message) => output.push(message),
				},
			),
		).toBe(1);
		const text = output.join("\n");
		expect(text).not.toContain("failed (Error)");
		expect(text).toContain("do not rerun");
	});
});

test("formats claimed IDs", () => {
	expect(formatClaimed([{ id: "DL-377", date: "2026-09-29" }])).toBe(
		"DL-377 (claimed 2026-09-29)",
	);
});

test("uses a non-empty token file before the environment value", async () => {
	const directory = mkdtempSync(join(tmpdir(), "dl-claim-test-"));
	temporaryDirectories.push(directory);
	const path = join(directory, "token");
	writeFileSync(path, " file-token \n");
	expect(
		await readDlClaimToken({
			DL_CLAIM_TOKEN: "env",
			DL_CLAIM_TOKEN_FILE: path,
		}),
	).toBe("file-token");
	const fallbackPath = join(directory, "empty-token");
	writeFileSync(fallbackPath, " \n\t ");
	expect(
		await readDlClaimToken({
			DL_CLAIM_TOKEN: "env-token",
			DL_CLAIM_TOKEN_FILE: fallbackPath,
		}),
	).toBe("env-token");
	expect(() => validateDlClaimToken("bad\ntoken")).toThrow("printable ASCII");
});

test("rejects a token containing an interior newline without logging it", async () => {
	const directory = mkdtempSync(join(tmpdir(), "dl-claim-cli-test-"));
	temporaryDirectories.push(directory);
	const configPath = join(directory, "config.json");
	writeFileSync(configPath, JSON.stringify(oneSurface));
	const output: string[] = [];
	const result = await runOnce(
		["--config", configPath, "--ref", "none", "--lane", "feature/b"],
		{ token: "safe\nsecret", err: (message) => output.push(message) },
	);
	expect(result).toBe(2);
	expect(output.join("\n")).not.toContain("safe\nsecret");
});
