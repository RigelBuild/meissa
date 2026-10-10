import { afterAll, describe, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// The CLI exit contract: 0 pass, 1 preflight failed, 2 could not evaluate.
// A fake `gh` on PATH stands in for GitHub so no case makes a network call.
const fakeBin = mkdtempSync(join(tmpdir(), "preflight-gh-"));
afterAll(() => rmSync(fakeBin, { recursive: true, force: true }));

const fakeGh = (script: string) => {
	writeFileSync(join(fakeBin, "gh"), `#!/bin/sh\n${script}\n`);
	chmodSync(join(fakeBin, "gh"), 0o755);
};

const run = (env: Record<string, string>) =>
	Bun.spawnSync(["bun", join(import.meta.dir, "index.ts")], {
		env: { PATH: `${fakeBin}:${process.env.PATH}`, ...env },
		stdout: "pipe",
		stderr: "pipe",
	}).exitCode;

describe("renovate-preflight CLI exit codes", () => {
	test("a missing REPO exits 2", () => {
		expect(run({ RENOVATE_TOKEN: "t" })).toBe(2);
	});

	test("a REPO without an owner/name split exits 2", () => {
		expect(run({ REPO: "just-a-name", RENOVATE_TOKEN: "t" })).toBe(2);
	});

	test("an empty RENOVATE_TOKEN exits 1", () => {
		expect(run({ REPO: "o/n", RENOVATE_TOKEN: "" })).toBe(1);
	});

	test("a probe GitHub answers exits 0", () => {
		fakeGh(
			`echo '{"data":{"repository":{"nameWithOwner":"o/n","defaultBranchRef":{"name":"main"}}}}'`,
		);
		expect(run({ REPO: "o/n", RENOVATE_TOKEN: "t" })).toBe(0);
	});

	test("a bad-credentials probe exits 1", () => {
		fakeGh(
			`echo 'gh: Bad credentials (HTTP 401)' >&2\necho '{"message":"Bad credentials"}'\nexit 1`,
		);
		expect(run({ REPO: "o/n", RENOVATE_TOKEN: "t" })).toBe(1);
	});
});
