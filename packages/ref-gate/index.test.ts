import { afterEach, describe, expect, test } from "bun:test";
import {
	mkdirSync,
	mkdtempSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import {
	type Deps,
	findViolations,
	gitGrep,
	loadRefGateConfig,
	type RefGateConfig,
	runOnce,
} from "./index.ts";

const packageDirectory = fileURLToPath(new URL(".", import.meta.url));
const temporaryDirectoryRoot = join(packageDirectory, ".tmp-test");
const temporaryDirectories: string[] = [];

function makeConfig(): RefGateConfig {
	return {
		prefilter: { ere: "acme", ignoreCase: false },
		patterns: [{ source: "\\bacme\\b", flags: "" }],
		ignore: [],
		carveOutPaths: [],
		carveOutPrefixes: [],
		allowlist: {},
	};
}

function packageTemporaryDirectory(prefix: string): string {
	mkdirSync(temporaryDirectoryRoot, { recursive: true });
	return temporaryDirectory(temporaryDirectoryRoot, prefix);
}

function temporaryDirectory(parent: string, prefix: string): string {
	const directory = mkdtempSync(join(parent, prefix));
	temporaryDirectories.push(directory);
	return directory;
}

function configFile(contents: unknown): string {
	const directory = packageTemporaryDirectory("config-");
	const path = join(directory, "gate.json");
	writeFileSync(path, JSON.stringify(contents));
	return path;
}

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
	rmSync(temporaryDirectoryRoot, { recursive: true, force: true });
});

describe("loadRefGateConfig", () => {
	// An empty pattern list would make every scan clean.
	test("rejects an empty pattern list", () => {
		expect(() =>
			loadRefGateConfig(configFile({ ...makeConfig(), patterns: [] })),
		).toThrow("non-empty");
	});

	test("rejects unknown keys at the top level and in prefilter", () => {
		const base = makeConfig();
		expect(() =>
			loadRefGateConfig(configFile({ ...base, unexpected: true })),
		).toThrow('Unknown key "unexpected"');
		expect(() =>
			loadRefGateConfig(
				configFile({ ...base, prefilter: { ...base.prefilter, extra: true } }),
			),
		).toThrow('Unknown key "extra"');
	});

	test("rejects malformed JavaScript regular expressions", () => {
		const base = makeConfig();
		expect(() =>
			loadRefGateConfig(
				configFile({ ...base, patterns: [{ source: "(", flags: "" }] }),
			),
		).toThrow("Invalid regular expression");
	});

	test("rejects fields with the wrong type", () => {
		const base = makeConfig();
		expect(() =>
			loadRefGateConfig(
				configFile({
					...base,
					prefilter: { ere: "acme", ignoreCase: "false" },
				}),
			),
		).toThrow("prefilter.ignoreCase must be a boolean");
	});
});

describe("findViolations", () => {
	test("throws on a hit it cannot parse instead of skipping it", () => {
		expect(() => findViolations(makeConfig(), ["no separators acme"])).toThrow(
			"unparsable grep hit",
		);
	});

	test("skips exact carve-outs and matching path prefixes", () => {
		const config = {
			...makeConfig(),
			carveOutPaths: ["generated/one.txt"],
			carveOutPrefixes: ["generated/docs/"],
		};
		expect(
			findViolations(config, [
				"generated/one.txt:1:acme",
				"generated/docs/page.txt:2:acme",
				"docs/page.txt:3:acme",
			]),
		).toEqual([{ file: "docs/page.txt", line: 3, text: "acme" }]);
	});

	test("skips an allowlisted exact path", () => {
		const config = {
			...makeConfig(),
			allowlist: { "fixtures/example.txt": "Synthetic reference" },
		};
		expect(
			findViolations(config, [
				"fixtures/example.txt:1:acme",
				"docs/example.txt:2:acme",
			]),
		).toEqual([{ file: "docs/example.txt", line: 2, text: "acme" }]);
	});

	test("removes the compound name before matching but keeps a bare name", () => {
		const config = { ...makeConfig(), ignore: ["acme-gate"] };
		expect(
			findViolations(config, [
				"docs/compound.txt:1:acme-gate",
				"docs/bare.txt:2:acme-gate detects acme",
			]),
		).toEqual([
			{ file: "docs/bare.txt", line: 2, text: "acme-gate detects acme" },
		]);
	});

	test("matches case-insensitive patterns and parses colons in paths and text", () => {
		const config = {
			...makeConfig(),
			patterns: [{ source: "\\bacme\\b", flags: "i" }],
		};
		expect(
			findViolations(config, [
				"docs:notes/file.txt\0" + "42\0" + "ACME: see docs:notes again",
			]),
		).toEqual([
			{
				file: "docs:notes/file.txt",
				line: 42,
				text: "ACME: see docs:notes again",
			},
		]);
	});
});

describe("runOnce", () => {
	function fakeDeps(
		hits: string[] | Error,
		logs: string[],
		errors: string[],
		seenPrefilters: RefGateConfig["prefilter"][] = [],
	): Deps {
		return {
			grep: async (prefilter) => {
				seenPrefilters.push(prefilter);
				if (hits instanceof Error) throw hits;
				return hits;
			},
			log: (message) => logs.push(message),
			err: (message) => errors.push(message),
		};
	}

	test("returns 0 for a clean scan", async () => {
		const logs: string[] = [];
		const errors: string[] = [];
		expect(await runOnce(fakeDeps([], logs, errors), makeConfig())).toBe(0);
		expect(logs).toEqual(["ref-gate: clean"]);
		expect(errors).toEqual([]);
	});

	test("returns 1 and prints each violation plus the remediation hint", async () => {
		const logs: string[] = [];
		const errors: string[] = [];
		const config = {
			...makeConfig(),
			patterns: [{ source: "\\bacme\\b", flags: "i" }],
			remediationDoc: "docs/reference-policy.md",
		};
		const code = await runOnce(
			fakeDeps(["docs/one.md:7:acme", "docs/two.md:8:ACME"], logs, errors),
			config,
		);
		expect(code).toBe(1);
		expect(errors).toEqual([
			"docs/one.md:7: acme",
			"docs/two.md:8: ACME",
			"See docs/reference-policy.md.",
		]);
	});

	test("returns 2 when the scan fails", async () => {
		const logs: string[] = [];
		const errors: string[] = [];
		expect(
			await runOnce(
				fakeDeps(new Error("not a git tree"), logs, errors),
				makeConfig(),
			),
		).toBe(2);
		expect(errors.join("\n")).toContain("not a git tree");
	});

	test("passes the POSIX ERE prefilter verbatim instead of deriving it", async () => {
		const logs: string[] = [];
		const errors: string[] = [];
		const seen: RefGateConfig["prefilter"][] = [];
		const config = {
			...makeConfig(),
			prefilter: { ere: "\\bacme\\b", ignoreCase: true },
		};
		expect(await runOnce(fakeDeps([], logs, errors, seen), config)).toBe(0);
		expect(seen).toEqual([config.prefilter]);
	});
});

describe("CLI usage", () => {
	test("returns usage exit code when the config flag is missing", () => {
		const result = Bun.spawnSync({
			cmd: ["bun", join(packageDirectory, "index.ts")],
			cwd: packageDirectory,
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(2);
		expect(new TextDecoder().decode(result.stderr)).toContain(
			"Usage: ref-gate --config <path>",
		);
	});

	test("returns internal-error exit code for an invalid config", () => {
		const configPath = configFile({ ...makeConfig(), unexpected: true });
		const result = Bun.spawnSync({
			cmd: ["bun", join(packageDirectory, "index.ts"), "--config", configPath],
			cwd: packageDirectory,
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(2);
		expect(new TextDecoder().decode(result.stderr)).toContain("Unknown key");
	});
});
function trackedRepository(prefix: string, files: Record<string, string>) {
	const repository = join(packageTemporaryDirectory(prefix), "repo");
	mkdirSync(repository);
	const git = (...args: string[]) =>
		expect(
			Bun.spawnSync({
				cmd: ["git", "-C", repository, ...args],
				stdout: "pipe",
				stderr: "pipe",
			}).exitCode,
		).toBe(0);
	git("init", "-q");
	for (const [name, text] of Object.entries(files)) {
		writeFileSync(join(repository, name), text);
		git("add", name);
	}
	return repository;
}

describe("Git prefilter", () => {
	test("passes the POSIX ERE and case option to git grep", () => {
		const repository = trackedRepository("prefilter-", {
			"fixture.txt": "Acme reference\nclean\n",
		});
		expect(gitGrep(repository, { ere: "acme", ignoreCase: true })).toEqual([
			"fixture.txt\0" + "1\0" + "Acme reference",
		]);
	});

	test("returns no hits when git grep finds no match", () => {
		const repository = trackedRepository("nomatch-", {
			"fixture.txt": "clean\n",
		});
		expect(gitGrep(repository, { ere: "acme", ignoreCase: false })).toEqual([]);
	});

	// Exit 1 is the only clean no-match; any other failure must not read as clean.
	test("throws on a git grep error instead of passing", () => {
		const repository = trackedRepository("badere-", {
			"fixture.txt": "acme\n",
		});
		expect(() =>
			gitGrep(repository, { ere: "acme(", ignoreCase: false }),
		).toThrow("git grep exited");
		const notRepository = packageTemporaryDirectory("norepo-");
		expect(() =>
			gitGrep(notRepository, { ere: "acme", ignoreCase: false }),
		).toThrow("not the top level of a git repository");
	});

	// A path's LF must not split it into a fragment that looks like a carved-out file.
	test("a newline in a tracked path cannot spoof a carve-out", () => {
		const repository = trackedRepository("newline-", {
			"sub\ngate.json": "acme here\n",
			"other.txt": "acme there\n",
		});
		const config = { ...makeConfig(), carveOutPaths: ["gate.json"] };
		const hits = gitGrep(repository, config.prefilter);
		expect(findViolations(config, hits).map((ref) => ref.file)).toEqual([
			"other.txt",
			"sub\ngate.json",
		]);
	});

	// Neither a .gitattributes mark nor a NUL byte may hide a file from the scan.
	test("scans files git would treat as binary", () => {
		const repository = trackedRepository("binary-", {
			".gitattributes": "marked.md binary\n",
			"marked.md": "acme marked\n",
			"nul.md": "acme nul\n\0\n",
		});
		const config = makeConfig();
		const hits = gitGrep(repository, config.prefilter);
		expect(findViolations(config, hits).map((ref) => ref.file)).toEqual([
			"marked.md",
			"nul.md",
		]);
	});

	test("accepts a root reached through a symlink", () => {
		const repository = trackedRepository("symlinked-", {
			"fixture.txt": "acme\n",
		});
		const link = join(packageTemporaryDirectory("link-"), "repo");
		symlinkSync(repository, link);
		expect(gitGrep(link, { ere: "acme", ignoreCase: false })).toHaveLength(1);
	});
});

describe("CLI end to end", () => {
	test("scans staged files in a temporary Git repository", () => {
		const workspace = packageTemporaryDirectory("ref-gate-");
		const repository = join(workspace, "repo");
		mkdirSync(repository);
		const fixture = join(repository, "fixture.txt");
		const configPath = join(workspace, "gate.json");

		writeFileSync(configPath, JSON.stringify(makeConfig()));

		const init = Bun.spawnSync({
			cmd: ["git", "-C", repository, "init", "-q"],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(init.exitCode).toBe(0);

		const runCli = () =>
			Bun.spawnSync({
				cmd: [
					"bun",
					join(packageDirectory, "index.ts"),
					"--config",
					configPath,
				],
				cwd: packageDirectory,
				env: { ...process.env, GATE_ROOT: repository },
				stdout: "pipe",
				stderr: "pipe",
			});

		writeFileSync(fixture, "reference to acme\n");
		const addViolation = Bun.spawnSync({
			cmd: ["git", "-C", repository, "add", "fixture.txt"],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(addViolation.exitCode).toBe(0);
		const violatingRun = runCli();
		expect(violatingRun.exitCode).toBe(1);
		expect(new TextDecoder().decode(violatingRun.stderr)).toContain(
			"fixture.txt:1: reference to acme",
		);

		writeFileSync(fixture, "clean content\n");
		const addClean = Bun.spawnSync({
			cmd: ["git", "-C", repository, "add", "fixture.txt"],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(addClean.exitCode).toBe(0);
		expect(runCli().exitCode).toBe(0);
	});
});
