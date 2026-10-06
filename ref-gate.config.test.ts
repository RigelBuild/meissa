import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import {
	findViolations,
	gitGrep,
	loadRefGateConfig,
} from "./packages/ref-gate/index.ts";

const directories: string[] = [];

afterEach(() => {
	for (const directory of directories.splice(0))
		rmSync(directory, { recursive: true, force: true });
});

// Joined at runtime so this file never holds a token its own gate forbids.
const name = ["or", "ion"].join("");
const former = ["seal", "ed"].join("");
const issue = ["SE", "A-12"].join("");

// Each JS pattern must survive the ERE prefilter, or git grep drops its hits unseen.
test("the prefilter keeps a hit for every configured pattern", () => {
	const config = loadRefGateConfig(
		join(import.meta.dir, "ref-gate.config.json"),
	);
	const samples = [
		`the ${name} repo`,
		`see ${former}/apps`,
		`${former} tools/gate.ts`,
		`the ${former}-monorepo`,
		`${former}'s docs`,
		`the ${former} platform`,
		`see ${issue}`,
	];
	expect(samples.length).toBe(config.patterns.length);

	const repository = mkdtempSync(join(import.meta.dir, ".ref-gate-test-"));
	directories.push(repository);
	writeFileSync(join(repository, "fixture.txt"), `${samples.join("\n")}\n`);
	for (const args of [
		["init", "-q"],
		["add", "fixture.txt"],
	]) {
		const result = Bun.spawnSync({
			cmd: ["git", "-C", repository, ...args],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(0);
	}

	const hits = gitGrep(repository, config.prefilter);
	expect(findViolations(config, hits).map((ref) => ref.line)).toEqual(
		samples.map((_, index) => index + 1),
	);
});
