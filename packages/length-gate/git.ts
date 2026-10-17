import { resolve as resolvePath } from "node:path";

export type Deps = {
	root: string;
	resolve: (rev: string) => Promise<string | null>;
	fetchBase: (branch: string) => Promise<string>;
	mergeBase: (a: string, b: string) => Promise<string>;
	diff: (base: string, head: string) => Promise<string>;
	readAt: (rev: string, path: string) => Promise<string | null>;
	log: (msg: string) => void;
	err: (msg: string) => void;
	listFiles: (rev: string) => Promise<string[]>;
};

const textDecoder = new TextDecoder();

function gitResult(root: string, args: readonly string[]) {
	return Bun.spawnSync({
		cmd: ["git", "-C", root, ...args],
		stdout: "pipe",
		stderr: "pipe",
	});
}

function git(root: string, ...args: string[]): string {
	const result = gitResult(root, args);
	if (result.exitCode !== 0) {
		const detail = textDecoder.decode(result.stderr).trim();
		throw new Error(
			detail || `git ${args[0] ?? "command"} exited ${result.exitCode}`,
		);
	}
	return textDecoder.decode(result.stdout).trimEnd();
}

function nullableGit(root: string, ...args: string[]): string | null {
	const result = gitResult(root, args);
	return result.exitCode === 0
		? textDecoder.decode(result.stdout).trim()
		: null;
}

export function gitTopLevel(root: string): string {
	return git(resolvePath(root), "rev-parse", "--show-toplevel");
}

export function createGitDeps(
	root: string,
	log: (message: string) => void = console.log,
	err: (message: string) => void = console.error,
	// The commit under test; shallow deepening stops once it shares history with the base.
	headRevision = "HEAD",
): Deps {
	const repository = resolvePath(root);
	const resolve = async (revision: string): Promise<string | null> =>
		nullableGit(
			repository,
			"rev-parse",
			"--verify",
			"--quiet",
			`${revision}^{commit}`,
		);
	const mergeBase = async (base: string, head: string): Promise<string> => {
		const result = nullableGit(repository, "merge-base", base, head);
		if (result === null)
			throw new Error(`no merge base for ${base} and ${head}`);
		return result;
	};
	const diff = async (base: string, head: string): Promise<string> => {
		// Pin output-shaping config so repo settings cannot empty or reshape the diff.
		const result = gitResult(repository, [
			"-c",
			"core.quotePath=false",
			"-c",
			"diff.suppressBlankEmpty=false",
			"diff",
			"--no-color",
			"--no-ext-diff",
			"--no-textconv",
			"--submodule=short",
			"--src-prefix=a/",
			"--dst-prefix=b/",
			`${base}...${head}`,
		]);
		if (result.exitCode !== 0)
			throw new Error(
				textDecoder.decode(result.stderr).trim() ||
					`git diff exited ${result.exitCode}`,
			);
		return textDecoder.decode(result.stdout);
	};
	const readAt = async (
		revision: string,
		path: string,
	): Promise<string | null> => {
		const result = gitResult(repository, ["show", `${revision}:${path}`]);
		return result.exitCode === 0 ? textDecoder.decode(result.stdout) : null;
	};
	const listFiles = async (revision: string): Promise<string[]> => {
		const result = gitResult(repository, ["ls-tree", "-r", "-z", revision]);
		if (result.exitCode !== 0) {
			const detail = textDecoder.decode(result.stderr).trim();
			throw new Error(detail || `git ls-tree exited ${result.exitCode}`);
		}
		// Keep blobs only; a submodule gitlink has no readable content.
		return textDecoder
			.decode(result.stdout)
			.split("\0")
			.flatMap((entry) => {
				const tab = entry.indexOf("\t");
				if (tab === -1 || entry.split(" ")[1] !== "blob") return [];
				return [entry.slice(tab + 1)];
			});
	};
	const fetchBase = async (branch: string): Promise<string> => {
		const shallow = nullableGit(
			repository,
			"rev-parse",
			"--is-shallow-repository",
		);
		if (shallow === null)
			throw new Error("cannot determine whether repository is shallow");
		const refspec = `+refs/heads/${branch}:refs/remotes/origin/${branch}`;
		const fetch = (...depth: string[]) => {
			const result = gitResult(repository, [
				"fetch",
				"--no-tags",
				...depth,
				"origin",
				refspec,
			]);
			if (result.exitCode !== 0)
				throw new Error(
					textDecoder.decode(result.stderr).trim() ||
						`git fetch exited ${result.exitCode}`,
				);
		};
		for (let attempt = 0; attempt < 20; attempt += 1) {
			try {
				if (shallow === "true")
					fetch(attempt === 0 ? "--unshallow" : "--deepen=100");
				else fetch();
			} catch (error) {
				if (!(shallow === "true" && attempt === 0)) throw error;
				fetch("--deepen=100");
			}
			const base = nullableGit(
				repository,
				"rev-parse",
				"--verify",
				"--quiet",
				`refs/remotes/origin/${branch}^{commit}`,
			);
			if (base === null)
				throw new Error(`fetched branch origin/${branch} did not resolve`);
			const head = nullableGit(
				repository,
				"rev-parse",
				"--verify",
				"--quiet",
				`${headRevision}^{commit}`,
			);
			if (
				head !== null &&
				nullableGit(repository, "merge-base", base, head) !== null
			)
				return base;
			if (
				nullableGit(repository, "rev-parse", "--is-shallow-repository") !==
				"true"
			)
				throw new Error(
					"no merge base after fetching complete repository history",
				);
		}
		throw new Error("no merge base after deepening shallow history");
	};

	return {
		root: repository,
		resolve,
		fetchBase,
		mergeBase,
		diff,
		readAt,
		log,
		err,
		listFiles,
	};
}
