import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { watch } from "node:fs";
import { chmod, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
import { $ } from "bun";
import { golangciLintTag, PIN_FILE } from "./refresh-go-analysis-hashes.ts";

const repoRoot = join(import.meta.dir, "../..");
const SCRIPT_REL = "tools/renovate/refresh-go-analysis-hashes.ts";
const SCRIPT_SOURCE = join(import.meta.dir, "refresh-go-analysis-hashes.ts");
const CALLS_FILE = ".scratch/nix-calls.jsonl";
const TOOL_NAMES = ["nilaway", "golangci-lint"] as const;
const HERMETIC_ENV = {
	...process.env,
	GIT_CONFIG_GLOBAL: "/dev/null",
	GIT_CONFIG_SYSTEM: "/dev/null",
	GIT_AUTHOR_NAME: "Go analysis test",
	GIT_AUTHOR_EMAIL: "go-analysis@example.invalid",
	GIT_COMMITTER_NAME: "Go analysis test",
	GIT_COMMITTER_EMAIL: "go-analysis@example.invalid",
};

const STUB_NIX = `#!/usr/bin/env bun
import { appendFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.slice(2);
const pinText = readFileSync("tools/toolchain/go-analysis-pins.nix", "utf8");
const names = ["nilaway", "golangci-lint"];
function blockFor(tool) {
  const lines = pinText.split("\\n");
  const start = lines.findIndex((line) => line.trimStart().startsWith(tool + " = {"));
  const end = lines.findIndex((line, index) => index > start && line.trim() === "};");
  if (start < 0 || end < 0) throw new Error("missing pin block: " + tool);
  return lines.slice(start, end + 1).join("\\n");
}
function field(block, name) {
  const match = block.match(new RegExp("^\\\\s*" + name + " = \\"([^\\"]+)\\"", "m"));
  if (!match || !match[1]) throw new Error("missing pin field: " + name);
  return match[1];
}
function drvPath(tool, kind) {
  const block = blockFor(tool);
  const id = createHash("sha256").update([tool, kind, field(block, "version"), field(block, "hash")].join("|")).digest("hex").slice(0, 12);
  return kind === "src" ? "/nix/store/" + id + "-source.drv" : "/nix/store/" + id + "-" + tool + "-" + field(block, "version") + "-go-modules.drv";
}
async function record(call) {
  if (process.env.NIX_CALLS) await appendFile(process.env.NIX_CALLS, JSON.stringify(call) + "\\n");
}
if (args[0] === "eval") {
  const expression = args[args.indexOf("--expr") + 1] || "";
  const tool = names.find((name) => expression.includes("tools." + name + "."));
  const kind = expression.includes(".src.drvPath") ? "src" : expression.includes(".goModules.drvPath") ? "goModules" : "";
  if (!tool || !kind) throw new Error("unexpected eval expression: " + expression);
  const path = drvPath(tool, kind);
  await record({ action: "eval", expression, path });
  process.stdout.write(path + "\\n");
  process.exit(0);
}
if (args[0] === "build") {
  const expression = args[args.indexOf("--expr") + 1] || "";
  const tool = names.find((name) => expression.includes("tools." + name));
  if (!tool) throw new Error("unexpected build expression: " + expression);
  await record({ action: "build", expression });
  if (process.env.STUB_NIX_BLOCK_BUILD === "1") {
    const readyDir = process.env.STUB_NIX_READY_DIR;
    if (!readyDir) throw new Error("missing readiness dir");
    await Bun.write(join(readyDir, "nix-ready-" + process.pid), "ready\\n");
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0);
  }
  if (process.env.STUB_NIX_NO_GOT === "1") {
    process.stderr.write("error: build failed without a hash mismatch\\n");
    process.exit(1);
  }
  const block = blockFor(tool);
  const sourceHash = field(block, "hash");
  const vendorHash = field(block, "vendorHash");
  const sourceGot = "sha256-stub-" + tool + "-source";
  const vendorGot = "sha256-stub-" + tool + "-vendor";
  const kind = sourceHash !== sourceGot ? "src" : vendorHash !== vendorGot ? "goModules" : "";
  if (!kind) process.exit(0);
  const path = drvPath(tool, kind);
  process.stderr.write("error: hash mismatch in fixed-output derivation '/nix/store/00000000000000000000-foreign-source.drv':\\n         specified: sha256-fake\\n            got:    sha256-foreign\\n");
  process.stderr.write("error: hash mismatch in fixed-output derivation '" + path + "':\\n         specified: sha256-fake\\n            got:    " + (kind === "src" ? sourceGot : vendorGot) + "\\n");
  process.exit(1);
}
throw new Error("unexpected nix args: " + args.join(" "));
`;

type Tool = (typeof TOOL_NAMES)[number];
type Call = {
	readonly action: "eval" | "build";
	readonly expression: string;
	readonly path?: string;
};
let scratch = "";
let repo = "";
let nilawayFetchUrl = "";
let nilawayRev = "";

function block(text: string, tool: Tool): string {
	const lines = text.split("\n");
	const start = lines.findIndex((line) =>
		line.trimStart().startsWith(`${tool} = {`),
	);
	const end = lines.findIndex(
		(line, index) => index > start && line.trim() === "};",
	);
	if (start < 0 || end < 0) throw new Error(`missing ${tool} pin block`);
	return lines.slice(start, end + 1).join("\n");
}

function replaceBlock(text: string, tool: Tool, updated: string): string {
	const original = block(text, tool);
	const start = text.indexOf(original);
	if (start < 0) throw new Error(`cannot locate ${tool} pin block`);
	return `${text.slice(0, start)}${updated}${text.slice(start + original.length)}`;
}

function setField(
	text: string,
	tool: Tool,
	field: string,
	value: string,
): string {
	const original = block(text, tool);
	const pattern = new RegExp(
		`(^\\s*${field}\\s*=\\s*")[^"]*("\\s*;\\s*$)`,
		"m",
	);
	if (!pattern.test(original))
		throw new Error(`missing ${field} in ${tool} fixture`);
	return replaceBlock(
		text,
		tool,
		original.replace(
			pattern,
			(_line, before: string, after: string) => `${before}${value}${after}`,
		),
	);
}

async function writeFixture(
	root: string,
	relativePath: string,
	contents: string,
): Promise<void> {
	const path = join(root, relativePath);
	await mkdir(dirname(path), { recursive: true });
	await Bun.write(path, contents);
}

async function setupRepo(): Promise<void> {
	repo = join(scratch, "repo");
	await mkdir(join(repo, ".scratch"), { recursive: true });
	await Bun.write(join(repo, CALLS_FILE), "");
	await writeFixture(
		repo,
		PIN_FILE,
		await readFile(join(repoRoot, PIN_FILE), "utf8"),
	);
	await writeFixture(repo, SCRIPT_REL, await readFile(SCRIPT_SOURCE, "utf8"));
	const nixPath = join(repo, "stubbin", "nix");
	await writeFixture(repo, "stubbin/nix", STUB_NIX);
	await chmod(nixPath, 0o755);
	await $`git init -q -b main`.cwd(repo).env(HERMETIC_ENV).quiet();
	await $`git add -A`.cwd(repo).env(HERMETIC_ENV).quiet();
	await $`git commit -q -m baseline`.cwd(repo).env(HERMETIC_ENV).quiet();
	await $`git remote add origin .`.cwd(repo).env(HERMETIC_ENV).quiet();
	await $`git update-ref refs/remotes/origin/main main`
		.cwd(repo)
		.env(HERMETIC_ENV)
		.quiet();
}

async function setupNilawayUpstream(): Promise<void> {
	const upstream = join(scratch, "nilaway-upstream");
	await mkdir(upstream, { recursive: true });
	await Bun.write(join(upstream, "fixture.txt"), "fixture\n");
	const commitDate = "2026-01-02T23:30:00-0800";
	const env = {
		...HERMETIC_ENV,
		GIT_AUTHOR_DATE: commitDate,
		GIT_COMMITTER_DATE: commitDate,
	};
	await $`git init -q`.cwd(upstream).env(env).quiet();
	await $`git add fixture.txt`.cwd(upstream).env(env).quiet();
	await $`git commit -q -m upstream`.cwd(upstream).env(env).quiet();
	nilawayRev = (
		await $`git rev-parse HEAD`.cwd(upstream).env(env).text()
	).trim();
	nilawayFetchUrl = pathToFileURL(upstream).href;
}

async function runRefresh(
	extraEnv: Record<string, string> = {},
	baseBranch: string | undefined = "main",
) {
	const env: Record<string, string> = {
		...HERMETIC_ENV,
		PATH: `${join(repo, "stubbin")}:${process.env.PATH}`,
		NIX_CALLS: join(repo, CALLS_FILE),
		NILAWAY_FETCH_URL: nilawayFetchUrl,
		...extraEnv,
	};
	if (baseBranch === undefined) delete env.RENOVATE_BASE_BRANCH;
	else env.RENOVATE_BASE_BRANCH = baseBranch;
	return await $`bun ${SCRIPT_REL}`.cwd(repo).env(env).quiet().nothrow();
}

async function commitMainPin(contents: string): Promise<void> {
	await Bun.write(join(repo, PIN_FILE), contents);
	await $`git add ${PIN_FILE}`.cwd(repo).env(HERMETIC_ENV).quiet();
	await $`git commit -q -m changed-main-pin`
		.cwd(repo)
		.env(HERMETIC_ENV)
		.quiet();
	await $`git update-ref refs/remotes/origin/main HEAD`
		.cwd(repo)
		.env(HERMETIC_ENV)
		.quiet();
	await $`git update-ref refs/heads/main HEAD^`
		.cwd(repo)
		.env(HERMETIC_ENV)
		.quiet();
}

async function calls(): Promise<Call[]> {
	return (await readFile(join(repo, CALLS_FILE), "utf8"))
		.split("\n")
		.filter((line) => line.length > 0)
		.map((line) => JSON.parse(line) as Call);
}

async function waitForBuildReady(): Promise<number> {
	return await new Promise<number>((resolve, reject) => {
		const watcher = watch(join(repo, ".scratch"), (_event, filename) => {
			const name = filename?.toString() ?? "";
			if (!name.startsWith("nix-ready-")) return;
			watcher.close();
			const pid = Number(name.slice("nix-ready-".length));
			if (Number.isInteger(pid)) resolve(pid);
			else reject(new Error("invalid stub nix pid"));
		});
		watcher.on("error", reject);
	});
}

describe("Go analysis hash refresher", () => {
	beforeEach(async () => {
		scratch = await mkdtemp(join(tmpdir(), "go-analysis-test-"));
		await setupRepo();
		await setupNilawayUpstream();
	});

	afterEach(async () => {
		if (scratch) await rm(scratch, { recursive: true, force: true });
	});

	test("derives valid golangci-lint tags and rejects malformed versions", () => {
		expect(golangciLintTag("2.14.0")).toBe("v2.14.0");
		expect(golangciLintTag("2.14.0-rc.1")).toBe("v2.14.0-rc.1");
		expect(() => golangciLintTag("v2.14.0")).toThrow(
			"invalid golangci-lint version",
		);
		expect(() => golangciLintTag("2.14")).toThrow(
			"invalid golangci-lint version",
		);
	});

	test("refreshes a golangci-lint bump, preserves nilaway, and is idempotent", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		const otherBefore = block(initial, "nilaway");
		await Bun.write(
			join(repo, PIN_FILE),
			setField(initial, "golangci-lint", "version", "99.0.0"),
		);
		const first = await runRefresh();
		const after = await readFile(join(repo, PIN_FILE), "utf8");
		const golangci = block(after, "golangci-lint");
		expect(first.exitCode).toBe(0);
		expect(golangci).toContain('version = "99.0.0";');
		expect(golangci).toContain('tag = "v99.0.0";');
		expect(golangci).toContain('hash = "sha256-stub-golangci-lint-source";');
		expect(golangci).toContain(
			'vendorHash = "sha256-stub-golangci-lint-vendor";',
		);
		expect(block(after, "nilaway")).toBe(otherBefore);
		const recorded = await calls();
		expect(recorded.filter((call) => call.action === "build")).toHaveLength(2);
		expect(
			recorded
				.filter((call) => call.action === "eval")
				.map((call) => call.expression),
		).toEqual([
			expect.stringContaining("tools.golangci-lint.src.drvPath"),
			expect.stringContaining("tools.golangci-lint.goModules.drvPath"),
		]);
		const second = await runRefresh();
		expect(second.exitCode).toBe(0);
		expect(await readFile(join(repo, PIN_FILE), "utf8")).toBe(after);
	});

	test("refreshes nilaway hashes and UTC version from a negative-offset commit", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		const otherBefore = block(initial, "golangci-lint");
		await Bun.write(
			join(repo, PIN_FILE),
			setField(initial, "nilaway", "rev", nilawayRev),
		);
		const result = await runRefresh();
		const after = await readFile(join(repo, PIN_FILE), "utf8");
		const nilaway = block(after, "nilaway");
		expect(result.exitCode).toBe(0);
		expect(nilaway).toContain('version = "0-unstable-2026-01-03";');
		expect(nilaway).toContain(`rev = "${nilawayRev}";`);
		expect(nilaway).toContain('hash = "sha256-stub-nilaway-source";');
		expect(nilaway).toContain('vendorHash = "sha256-stub-nilaway-vendor";');
		expect(block(after, "golangci-lint")).toBe(otherBefore);
		expect(
			(await calls()).filter((call) => call.action === "build"),
		).toHaveLength(2);
	});

	test("selects origin/main when RENOVATE_BASE_BRANCH is unset", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		await commitMainPin(
			setField(initial, "golangci-lint", "version", "99.0.9"),
		);
		await Bun.write(
			join(repo, PIN_FILE),
			setField(initial, "golangci-lint", "version", "99.0.0"),
		);
		const result = await runRefresh({}, undefined);
		expect(result.exitCode).toBe(0);
		expect(
			block(await readFile(join(repo, PIN_FILE), "utf8"), "golangci-lint"),
		).toContain('tag = "v99.0.0";');
	});

	test("no-ops without building when pins match the base branch", async () => {
		const result = await runRefresh();
		expect(result.exitCode).toBe(0);
		expect(await calls()).toEqual([]);
	});

	test("restores the original pins when SIGTERM interrupts a hash build", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		const changed = setField(initial, "golangci-lint", "version", "2.14.8");
		await Bun.write(join(repo, PIN_FILE), changed);
		const ready = waitForBuildReady();
		const child = Bun.spawn([process.execPath, SCRIPT_REL], {
			cwd: repo,
			env: {
				...HERMETIC_ENV,
				PATH: `${join(repo, "stubbin")}:${process.env.PATH}`,
				NIX_CALLS: join(repo, CALLS_FILE),
				NILAWAY_FETCH_URL: nilawayFetchUrl,
				STUB_NIX_BLOCK_BUILD: "1",
				STUB_NIX_READY_DIR: join(repo, ".scratch"),
			},
			stdout: "pipe",
			stderr: "pipe",
		});
		let nixPid: number | undefined;
		try {
			nixPid = await ready;
			child.kill("SIGTERM");
			const [exitCode, stderr] = await Promise.all([
				child.exited,
				new Response(child.stderr).text(),
			]);
			expect(exitCode).not.toBe(0);
			expect(stderr).toContain("interrupted by SIGTERM");
			expect(await readFile(join(repo, PIN_FILE), "utf8")).toBe(changed);
		} finally {
			if (nixPid !== undefined) process.kill(nixPid, "SIGTERM");
		}
	});

	test("fails with a diagnostic and restores the changed pin on missing got hash", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		const changed = setField(initial, "golangci-lint", "version", "2.14.3");
		await Bun.write(join(repo, PIN_FILE), changed);
		const result = await runRefresh({ STUB_NIX_NO_GOT: "1" });
		expect(result.exitCode).not.toBe(0);
		expect(result.stderr.toString()).toContain("renovate-go-analysis:");
		expect(result.stderr.toString()).toContain("no got hash");
		expect(await readFile(join(repo, PIN_FILE), "utf8")).toBe(changed);
	});

	test("fails with the stable prefix on nilaway fetch errors and restores pins", async () => {
		const initial = await readFile(join(repo, PIN_FILE), "utf8");
		const changed = setField(initial, "nilaway", "rev", nilawayRev);
		await Bun.write(join(repo, PIN_FILE), changed);
		const result = await runRefresh({
			NILAWAY_FETCH_URL: "file:///missing-nilaway-fixture",
		});
		expect(result.exitCode).not.toBe(0);
		expect(result.stderr.toString()).toContain(
			"renovate-go-analysis: nilaway revision fetch failed",
		);
		expect(await readFile(join(repo, PIN_FILE), "utf8")).toBe(changed);
	});
});
