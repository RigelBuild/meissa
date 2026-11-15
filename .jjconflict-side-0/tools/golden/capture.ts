#!/usr/bin/env bun
import {
	existsSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

type Kind = "jsx" | "tsx" | "astro" | "markdown";
type TextScan = (text: string) => unknown;
type MarkdownCompare = (before: string, after: string) => { kind: string };

function isKind(value: string | undefined): value is Kind {
	return (
		value === "jsx" ||
		value === "tsx" ||
		value === "astro" ||
		value === "markdown"
	);
}

function isTextScan(value: unknown): value is TextScan {
	return typeof value === "function";
}

function isMarkdownCompare(value: unknown): value is MarkdownCompare {
	return typeof value === "function";
}

const usage =
	"Usage: bun tools/golden/capture.ts <revision> <jsx|tsx|astro|markdown>";
const extensions: Record<Kind, string> = {
	jsx: ".tsx",
	tsx: ".tsx",
	astro: ".astro",
	markdown: ".md",
};
const modules: Record<Kind, string> = {
	jsx: "jsx.ts",
	tsx: "jsx.ts",
	astro: "astro.ts",
	markdown: "markdown.ts",
};
const exports: Record<Kind, string> = {
	jsx: "scanJsx",
	tsx: "scanJsx",
	astro: "scanAstro",
	markdown: "markdownFingerprint",
};

async function main(args: readonly string[]): Promise<void> {
	const kindArg = args[1];
	if (args.length !== 2 || !args[0] || !isKind(kindArg)) {
		console.error(usage);
		process.exitCode = 2;
		return;
	}

	const root = resolve(import.meta.dir, "../..");
	const revision = args[0];
	const kind = kindArg;
	const inputDir = join(root, "go/commentscan/testdata/corpus", kind);
	const goldenDir = join(root, "go/commentscan/testdata/ts-golden", kind);
	const scratch = mkdtempSync(join(tmpdir(), "meissa-golden-"));
	const worktree = join(scratch, "worktree");
	let added = false;
	const git = (...gitArgs: string[]): string => {
		const result = Bun.spawnSync({
			cmd: ["git", "-C", root, ...gitArgs],
			stdout: "pipe",
			stderr: "pipe",
		});
		if (result.exitCode !== 0) {
			throw new Error(
				new TextDecoder().decode(result.stderr).trim() ||
					`git ${gitArgs.join(" ")} failed`,
			);
		}
		return new TextDecoder().decode(result.stdout).trim();
	};

	try {
		const commit = git("rev-parse", "--verify", `${revision}^{commit}`);
		git("worktree", "add", "--detach", worktree, commit);
		added = true;
		const dependencies = join(root, "node_modules");
		if (existsSync(dependencies)) {
			symlinkSync(dependencies, join(worktree, "node_modules"), "dir");
		}

		// The revision selects a worktree path at runtime, so static imports cannot load its scanner.
		const scannerPath = join(worktree, "packages/comment-scan", modules[kind]);
		const loaded: unknown = await import(pathToFileURL(scannerPath).href);
		if (typeof loaded !== "object" || loaded === null) {
			throw new Error(`revision ${commit} has no ${exports[kind]} export`);
		}
		const scanCandidate = Reflect.get(loaded, exports[kind]);
		if (!isTextScan(scanCandidate)) {
			throw new Error(`revision ${commit} does not export ${exports[kind]}`);
		}
		const scan = scanCandidate;
		const extension = extensions[kind];
		const inputs = readdirSync(inputDir, { withFileTypes: true })
			.filter((entry) => entry.isFile() && entry.name.endsWith(extension))
			.map((entry) => join(inputDir, entry.name))
			.sort();
		if (inputs.length === 0) {
			throw new Error(`no ${extension} inputs in ${inputDir}`);
		}
		mkdirSync(goldenDir, { recursive: true });
		for (const inputPath of inputs) {
			const text = readFileSync(inputPath, "utf8");
			const corpus = relative(root, inputPath).split(sep).join("/");
			const sourcePath = `packages/comment-scan/${modules[kind]}`;
			let golden: unknown;
			if (kind === "markdown") {
				const compareCandidate = Reflect.get(loaded, "compareMarkdown");
				if (!isMarkdownCompare(compareCandidate)) {
					throw new Error(`revision ${commit} does not export compareMarkdown`);
				}
				const verdict = compareCandidate(text, text);
				golden = {
					revision: commit,
					path: sourcePath,
					corpus,
					fingerprint: scan(text),
					comparison: { kind: verdict.kind },
				};
			} else {
				golden = {
					revision: commit,
					path: sourcePath,
					corpus,
					scan: scan(text),
				};
			}
			writeFileSync(
				join(goldenDir, `${basename(inputPath)}.json`),
				`${JSON.stringify(golden, null, 2)}\n`,
			);
		}
		console.log(`Captured ${inputs.length} ${kind} goldens at ${commit}`);
	} finally {
		try {
			if (added) git("worktree", "remove", "--force", worktree);
		} finally {
			rmSync(scratch, { recursive: true, force: true });
		}
	}
}

if (import.meta.main) {
	try {
		await main(process.argv.slice(2));
	} catch (error) {
		console.error(error instanceof Error ? error.message : String(error));
		process.exitCode = 1;
	}
}
