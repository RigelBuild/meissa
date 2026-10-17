import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadConfig } from "./config.ts";
import type { LengthGateConfig } from "./core.ts";
import { createGitDeps } from "./git.ts";
import { type GateEnv, runOnce } from "./run.ts";

const packageDirectory = fileURLToPath(new URL(".", import.meta.url));
const temporaryDirectories: string[] = [];
// Every git child, including the ones createGitDeps spawns, ignores the user's and system config.
const gitConfigDirectory = mkdtempSync(
	join(tmpdir(), "length-gate-gitconfig-"),
);
const emptyGlobalConfig = join(gitConfigDirectory, "config");
writeFileSync(emptyGlobalConfig, "");
Object.assign(process.env, {
	GIT_CONFIG_GLOBAL: emptyGlobalConfig,
	GIT_CONFIG_NOSYSTEM: "1",
	GIT_AUTHOR_NAME: "t",
	GIT_AUTHOR_EMAIL: "t@t",
	GIT_COMMITTER_NAME: "t",
	GIT_COMMITTER_EMAIL: "t@t",
});
afterAll(() => rmSync(gitConfigDirectory, { recursive: true, force: true }));
const COMMENT_CONFIG: LengthGateConfig = {
	extensions: [".ts"],
	exclude: [],
	docs: [],
};
const DOC_CONFIG: LengthGateConfig = {
	extensions: [],
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
	],
};

function temporaryDirectory(prefix: string): string {
	const directory = mkdtempSync(join(tmpdir(), `length-gate-${prefix}`));
	temporaryDirectories.push(directory);
	return directory;
}

function runGit(repository: string, ...args: string[]): string {
	const result = Bun.spawnSync({
		cmd: [
			"git",
			"-C",
			repository,
			"-c",
			"user.name=t",
			"-c",
			"user.email=t@t",
			...args,
		],
		stdout: "pipe",
		stderr: "pipe",
	});
	if (result.exitCode !== 0)
		throw new Error(new TextDecoder().decode(result.stderr));
	return new TextDecoder().decode(result.stdout).trim();
}

function writeRepositoryFile(
	repository: string,
	path: string,
	text: string,
): void {
	const absolutePath = join(repository, path);
	mkdirSync(dirname(absolutePath), { recursive: true });
	writeFileSync(absolutePath, text);
}

function trackedRepository(
	prefix: string,
	files: Readonly<Record<string, string>>,
): string {
	const repository = join(temporaryDirectory(prefix), "repo");
	mkdirSync(repository);
	const init = Bun.spawnSync({
		cmd: ["git", "-C", repository, "init", "-q", "-b", "main"],
		stdout: "pipe",
		stderr: "pipe",
	});
	if (init.exitCode !== 0)
		throw new Error(new TextDecoder().decode(init.stderr));
	for (const [path, text] of Object.entries(files))
		writeRepositoryFile(repository, path, text);
	runGit(repository, "add", "-A");
	runGit(repository, "commit", "-q", "-m", "base");
	return repository;
}

function commitChange(repository: string, message: string): void {
	runGit(repository, "add", "-A");
	runGit(repository, "commit", "-q", "-m", message);
}

function longComment(): string {
	return `${[1, 2, 3, 4, 5].map((line) => `// line ${line}`).join("\n")}\n`;
}

function configFile(value: unknown): string {
	const path = join(temporaryDirectory("config-"), "length-gate.json");
	writeFileSync(path, JSON.stringify(value));
	return path;
}

function capture(repository: string) {
	const logs: string[] = [];
	const errors: string[] = [];
	return {
		logs,
		errors,
		deps: createGitDeps(
			repository,
			(message) => logs.push(message),
			(message) => errors.push(message),
		),
	};
}

function expectRun(
	repository: string,
	env: GateEnv,
	config: LengthGateConfig = COMMENT_CONFIG,
) {
	const captured = capture(repository);
	return {
		...captured,
		result: runOnce(captured.deps, config, env),
	};
}

function makePullRequest(repository: string, path: string, text: string): void {
	runGit(repository, "checkout", "-q", "-b", "feature");
	writeRepositoryFile(repository, path, text);
	commitChange(repository, "feature change");
}

function configShape() {
	return {
		extensions: [".ts"],
		exclude: ["vendor/**"],
		docs: [
			{
				name: "spec",
				include: ["docs/specs/**/*.md"],
				exclude: [],
				target: 1500,
				markerAllowed: false,
			},
		],
	};
}

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0))
		rmSync(directory, { recursive: true, force: true });
});

describe("loadConfig", () => {
	test("loads the configured comment and document classes", () => {
		expect(loadConfig(configFile(configShape()))).toEqual(configShape());
	});

	test("rejects unknown keys at the root and inside document classes", () => {
		expect(() =>
			loadConfig(configFile({ ...configShape(), unexpected: true })),
		).toThrow("Unknown key");
		const config = configShape();
		const firstDoc = config.docs[0];
		if (firstDoc === undefined) throw new Error("missing test document class");
		expect(() =>
			loadConfig(
				configFile({ ...config, docs: [{ ...firstDoc, unexpected: true }] }),
			),
		).toThrow("Unknown key");
	});

	test("rejects wrong field types and unscanned extensions", () => {
		expect(() =>
			loadConfig(configFile({ ...configShape(), extensions: ".ts" })),
		).toThrow("extensions must be an array of strings");
		expect(() =>
			loadConfig(configFile({ ...configShape(), extensions: [".not-real"] })),
		).toThrow("no scanner");
	});
});

describe("runOnce pull request mode", () => {
	test("a pull request without base or fetch exits 2", async () => {
		const repository = trackedRepository("missing-base-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			head: "HEAD",
		});
		expect(await result.result).toBe(2);
	});

	test("a non-pull-request event logs a skip and exits 0", async () => {
		const repository = trackedRepository("push-skip-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, { event: "push", head: "HEAD" });
		expect(await result.result).toBe(0);
		expect(result.logs.join("\n")).toContain("skip");
	});

	test("merge mode rejects a head with only one parent", async () => {
		const repository = trackedRepository("single-parent-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			head: "HEAD",
			merge: true,
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("second parent");
	});

	test("merge mode judges from the head's first parent", async () => {
		const repository = trackedRepository("merge-base-", {
			"src/file.ts": "clean\n",
		});
		runGit(repository, "checkout", "-q", "-b", "feature");
		writeRepositoryFile(repository, "src/new.ts", longComment());
		commitChange(repository, "add long comment");
		runGit(repository, "checkout", "-q", "main");
		runGit(repository, "merge", "--no-ff", "-q", "feature", "-m", "merge");
		const result = await expectRun(repository, {
			event: "pull_request",
			head: "HEAD",
			merge: true,
		});
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("src/new.ts:1:");
	});

	test("empty, binary and non-ASCII files in a diff do not break the run", async () => {
		const repository = trackedRepository("odd-files-", {
			"src/file.ts": "clean\n",
			"old.png": "\u0000\u0001binary",
		});
		runGit(repository, "checkout", "-q", "-b", "feature");
		writeRepositoryFile(repository, "pkg/.gitkeep", "");
		writeRepositoryFile(repository, "src/empty.ts", "");
		writeRepositoryFile(repository, "img.png", "\u0000\u0002binary");
		writeRepositoryFile(repository, "src/café.ts", longComment());
		rmSync(join(repository, "old.png"));
		commitChange(repository, "odd files");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("src/café.ts:1:");
	});

	test("repository diff config cannot hide a change", async () => {
		const repository = trackedRepository("diff-config-", {
			"src/file.ts": "clean\n",
		});
		runGit(repository, "config", "diff.external", "true");
		runGit(repository, "config", "diff.srcPrefix", "x/");
		runGit(repository, "checkout", "-q", "-b", "feature");
		writeRepositoryFile(repository, "src/new.ts", longComment());
		commitChange(repository, "add long comment");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("src/new.ts:1:");
	});

	test("diff.submodule=diff cannot inline a submodule's files into the diff", async () => {
		const submodule = trackedRepository("submodule-", { "lib.ts": "clean\n" });
		const repository = trackedRepository("superproject-", {
			"src/file.ts": "clean\n",
		});
		runGit(
			repository,
			"-c",
			"protocol.file.allow=always",
			"submodule",
			"add",
			"-q",
			submodule,
			"vendor/sub",
		);
		commitChange(repository, "add submodule");
		runGit(repository, "checkout", "-q", "-b", "feature");
		writeRepositoryFile(submodule, "lib.ts", longComment());
		commitChange(submodule, "long comment");
		runGit(
			join(repository, "vendor/sub"),
			"-c",
			"protocol.file.allow=always",
			"pull",
			"-q",
			"origin",
			"main",
		);
		commitChange(repository, "bump submodule");
		runGit(repository, "config", "diff.submodule", "diff");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("a gitlink named like a gated file is not read", async () => {
		const repository = trackedRepository("pr-gitlink-", {
			"src/file.ts": "clean\n",
		});
		runGit(repository, "checkout", "-q", "-b", "feature");
		runGit(
			repository,
			"update-index",
			"--add",
			"--cacheinfo",
			"160000,1234567890123456789012345678901234567890,vendor/sub.ts",
		);
		runGit(repository, "commit", "-q", "-m", "gitlink");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("readAt treats a path as literal, never as a pathspec", async () => {
		const repository = trackedRepository("literal-", {
			"lit1.ts": "clean\n",
		});
		runGit(
			repository,
			"update-index",
			"--add",
			"--cacheinfo",
			"160000,1234567890123456789012345678901234567890,sub",
		);
		runGit(repository, "commit", "-q", "-m", "gitlink");
		const { deps } = capture(repository);
		expect(await deps.readAt("HEAD", "lit?.ts")).toBeNull();
		expect(await deps.readAt("HEAD", ":(top)sub")).toBeNull();
		expect(await deps.readAt("HEAD", "sub")).toBe("");
	});

	test("a gated file replaced by a gitlink is not read", async () => {
		const repository = trackedRepository("pr-typechange-", {
			"src/file.ts": "clean\n",
			"vendor/sub.ts": "clean\n",
		});
		runGit(repository, "checkout", "-q", "-b", "feature");
		runGit(repository, "rm", "-q", "--cached", "vendor/sub.ts");
		runGit(
			repository,
			"update-index",
			"--add",
			"--cacheinfo",
			"160000,1234567890123456789012345678901234567890,vendor/sub.ts",
		);
		runGit(repository, "commit", "-q", "-m", "file to gitlink");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("diff.suppressBlankEmpty cannot drop an added marker after a blank line", async () => {
		const repository = trackedRepository("blank-empty-", {
			"src/a.ts": "const a = 1;\n\nconst b = 2;\n",
		});
		runGit(repository, "config", "diff.suppressBlankEmpty", "true");
		runGit(repository, "checkout", "-q", "-b", "feature");
		const marked = `// LENGTH: frame header bit layout\n${longComment()}`;
		writeRepositoryFile(
			repository,
			"src/a.ts",
			`const a = 1;\n\nconst b = 2;\n${marked}`,
		);
		commitChange(repository, "add marked block");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
		writeRepositoryFile(repository, "src/empty.ts", "");
		commitChange(repository, "add empty file");
		const { deps } = capture(repository);
		expect(await deps.diff("main", "HEAD")).toContain(
			"diff --git a/src/empty.ts",
		);
	});

	test("fetches enough shallow history to find the merge base", async () => {
		const source = trackedRepository("shallow-source-", {
			"src/file.ts": "clean\n",
		});
		makePullRequest(source, "src/file.ts", longComment());
		const shallow = join(temporaryDirectory("shallow-clone-"), "repo");
		const clone = Bun.spawnSync({
			cmd: [
				"git",
				"clone",
				"-q",
				"--depth",
				"1",
				"--branch",
				"feature",
				`file://${source}`,
				shallow,
			],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(clone.exitCode).toBe(0);
		const result = await expectRun(shallow, {
			event: "pull_request",
			fetch: "main",
			head: "HEAD",
		});
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("src/file.ts:1:");
	});

	test("an empty diff exits 0 with nothing changed", async () => {
		const repository = trackedRepository("empty-diff-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "main",
		});
		expect(await result.result).toBe(0);
		expect(result.logs.join("\n")).toContain("nothing changed");
	});

	test("a violating changed file exits 1 and prints path:line", async () => {
		const repository = trackedRepository("finding-", {
			"src/file.ts": "clean\n",
		});
		makePullRequest(repository, "src/file.ts", longComment());
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "feature",
		});
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("src/file.ts:1:");
	});

	test("a valid marker lets a changed long comment pass", async () => {
		const repository = trackedRepository("finding-marked-", {
			"src/file.ts": "clean\n",
		});
		makePullRequest(
			repository,
			"src/file.ts",
			`// LENGTH: explains why this comment remains long\n${longComment()}`,
		);
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "feature",
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("an unresolved base exits 2 with the cause on stderr", async () => {
		const repository = trackedRepository("unresolved-base-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "missing-base",
			head: "HEAD",
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("missing-base");
	});

	test("an unresolved head exits 2 with the cause on stderr", async () => {
		const repository = trackedRepository("unresolved-head-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "missing-head",
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("missing-head");
	});

	test("a missing merge base exits 2 with the cause on stderr", async () => {
		const repository = trackedRepository("no-merge-base-", {
			"src/file.ts": "clean\n",
		});
		runGit(repository, "checkout", "--orphan", "unrelated");
		runGit(repository, "rm", "-rf", ".");
		writeRepositoryFile(repository, "other.ts", "unrelated\n");
		commitChange(repository, "unrelated root");
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "unrelated",
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("no merge base");
	});

	test("a missing changed-file object exits 2 and names the file", async () => {
		const repository = trackedRepository("missing-object-", {
			"src/file.ts": "clean\n",
		});
		makePullRequest(repository, "src/file.ts", longComment());
		const captured = capture(repository);
		const head = await captured.deps.resolve("feature");
		if (head === null) throw new Error("fixture head did not resolve");
		const deps = {
			...captured.deps,
			readAt: async (revision: string, path: string) =>
				revision === head ? null : captured.deps.readAt(revision, path),
		};
		const result = await runOnce(deps, COMMENT_CONFIG, {
			event: "pull_request",
			base: "main",
			head: "feature",
		});
		expect(result).toBe(2);
		expect(captured.errors.join("\n")).toContain("src/file.ts");
	});
});

describe("runOnce all mode", () => {
	test("finds an unchanged long block on both PR and push events", async () => {
		const repository = trackedRepository("all-existing-", {
			"src/old.ts": longComment(),
		});
		for (const event of ["pull_request", "push"]) {
			const result = await expectRun(repository, {
				event,
				head: "HEAD",
				all: true,
			});
			expect(await result.result).toBe(1);
			expect(result.errors.join("\n")).toContain("src/old.ts:1:");
		}
	});

	test("a clean tree exits 0", async () => {
		const repository = trackedRepository("all-clean-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "push",
			head: "HEAD",
			all: true,
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("a live marker exempts its block under --all", async () => {
		const repository = trackedRepository("all-marker-", {
			"src/file.ts": `// LENGTH: frame header bit layout\n${longComment()}`,
		});
		const result = await expectRun(repository, {
			event: "push",
			head: "HEAD",
			all: true,
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("a submodule gitlink and binary files do not break --all", async () => {
		const repository = trackedRepository("all-gitlink-", {
			"src/file.ts": "clean\n",
			"img.png": "\u0000\u0001binary",
		});
		// A gitlink names a commit in another repository, so its object is absent here.
		const head = "1234567890123456789012345678901234567890";
		runGit(
			repository,
			"update-index",
			"--add",
			"--cacheinfo",
			`160000,${head},vendor/sub`,
		);
		runGit(repository, "commit", "-q", "-m", "gitlink");
		const { deps } = capture(repository);
		expect((await deps.listFiles("HEAD")).sort()).toEqual([
			"img.png",
			"src/file.ts",
		]);
		const result = await expectRun(repository, {
			event: "push",
			head: "HEAD",
			all: true,
		});
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("an excluded long block is not reported", async () => {
		const repository = trackedRepository("all-excluded-", {
			"vendor/sdk.ts": longComment(),
		});
		const result = await expectRun(
			repository,
			{ event: "push", head: "HEAD", all: true },
			{ ...COMMENT_CONFIG, exclude: ["vendor/**"] },
		);
		expect(await result.result).toBe(0);
		expect(result.errors).toEqual([]);
	});

	test("skipClasses drops design records but keeps over-target specs", async () => {
		const design = `# Record\n\n${Array.from({ length: 3000 }, (_, index) => `record${index}`).join(" ")}\n`;
		const spec = `# Spec\n\n${Array.from({ length: 1600 }, (_, index) => `spec${index}`).join(" ")}\n`;
		const repository = trackedRepository("all-doc-classes-", {
			"docs/designs/record.md": design,
			"docs/specs/spec.md": spec,
		});
		const result = await expectRun(
			repository,
			{
				event: "push",
				head: "HEAD",
				all: true,
				skipClasses: ["design record"],
			},
			DOC_CONFIG,
		);
		expect(await result.result).toBe(1);
		expect(result.errors.join("\n")).toContain("docs/specs/spec.md");
		expect(result.errors.join("\n")).not.toContain("docs/designs/record.md");
	});

	test("an unknown skipped class exits 2", async () => {
		const repository = trackedRepository("all-unknown-class-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "push",
			head: "HEAD",
			all: true,
			skipClasses: ["unknown"],
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("unknown");
	});

	test("skipClasses without all exits 2", async () => {
		const repository = trackedRepository("skip-without-all-", {
			"src/file.ts": "clean\n",
		});
		const result = await expectRun(repository, {
			event: "pull_request",
			base: "main",
			head: "HEAD",
			skipClasses: ["spec"],
		});
		expect(await result.result).toBe(2);
		expect(result.errors.join("\n")).toContain("--all");
	});
});

describe("CLI end to end", () => {
	test("spawns the CLI and prints a failing finding", () => {
		const repository = trackedRepository("cli-", { "src/file.ts": "clean\n" });
		makePullRequest(repository, "src/file.ts", longComment());
		const config = configFile(configShape());
		const result = Bun.spawnSync({
			cmd: ["bun", join(packageDirectory, "index.ts"), "--config", config],
			cwd: packageDirectory,
			env: {
				...process.env,
				GATE_ROOT: repository,
				LENGTH_GATE_EVENT: "pull_request",
				LENGTH_GATE_BASE: "main",
				LENGTH_GATE_HEAD: "feature",
			},
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(1);
		expect(new TextDecoder().decode(result.stderr)).toContain("src/file.ts:1:");
	});
});
