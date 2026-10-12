import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const PACKAGES = "packages";

function declaredBins(manifest: { bin?: unknown }): string[] {
	const bin = manifest.bin;
	if (bin === undefined) return [];
	if (typeof bin === "string") return [bin];
	return Object.values(bin as Record<string, string>);
}

const bins = readdirSync(PACKAGES, { withFileTypes: true })
	.filter((entry) => entry.isDirectory())
	.flatMap((entry) =>
		declaredBins(
			JSON.parse(readFileSync(join(PACKAGES, entry.name, "package.json"), "utf8")),
		).map((bin) =>
			join(PACKAGES, entry.name, bin),
		),
	);

// A bin without a shebang fails with ENOEXEC when run from node_modules/.bin.
describe("package bins", () => {
	test("reads string, object, and absent bin declarations", () => {
		expect(declaredBins({ bin: "./cli.ts" })).toEqual(["./cli.ts"]);
		expect(declaredBins({ bin: { a: "./a.ts", b: "./b.ts" } })).toEqual([
			"./a.ts",
			"./b.ts",
		]);
		expect(declaredBins({})).toEqual([]);
	});

	test("at least one package declares a bin", () => {
		expect(bins.length).toBeGreaterThan(0);
	});

	test.each(bins)("%s starts with the bun shebang", (path) => {
		expect(readFileSync(path, "utf8").split("\n", 1)[0]).toBe(
			"#!/usr/bin/env bun",
		);
	});
});
