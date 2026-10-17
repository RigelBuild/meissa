import { type FileDiff, parseDiff } from "@rigelbuild/comment-scan";
import {
	type Finding,
	formatFinding,
	judgeFile,
	type LengthGateConfig,
	type Note,
} from "./core.ts";
import type { Deps } from "./git.ts";

export type { Deps } from "./git.ts";
export type GateEnv = {
	event: string;
	base?: string;
	fetch?: string;
	head: string;
	merge?: boolean;
	all?: boolean;
	skipClasses?: readonly string[];
};

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function withSkippedClasses(
	config: LengthGateConfig,
	names: readonly string[] | undefined,
	all: boolean | undefined,
): LengthGateConfig {
	if (names === undefined || names.length === 0) return config;
	if (!all) throw new Error("--skip-class is allowed only with --all");
	const classes = new Set(config.docs.map(({ name }) => name));
	for (const name of names) {
		if (!classes.has(name))
			throw new Error(`unknown document class ${JSON.stringify(name)}`);
	}
	const excluded = new Set(names);
	return {
		...config,
		docs: config.docs.filter(({ name }) => !excluded.has(name)),
	};
}

function isExcluded(path: string, config: LengthGateConfig): boolean {
	return config.exclude.some((glob) => new Bun.Glob(glob).match(path));
}

function allAddedFile(path: string, text: string): FileDiff {
	const lines = text.replace(/\r\n?/gu, "\n").split("\n");
	if (lines.at(-1) === "") lines.pop();
	return {
		path,
		oldPath: path,
		newPath: path,
		hunks:
			lines.length === 0
				? []
				: [
						{
							lines: lines.map((line, index) => ({
								kind: "+" as const,
								text: line,
								oldNo: 0,
								newNo: index + 1,
							})),
						},
					],
		added: false,
		deleted: false,
		binary: false,
		metadataOnly: false,
	};
}

function reportNotes(
	notes: readonly Note[],
	log: (message: string) => void,
): void {
	for (const note of notes)
		log(`${note.path}:${note.line}: unneeded LENGTH marker`);
}

function reportFindings(
	config: LengthGateConfig,
	findings: readonly Finding[],
	err: (message: string) => void,
): void {
	for (const finding of findings)
		for (const line of formatFinding(finding, config)) err(line);
}

export async function runOnce(
	deps: Deps,
	config: LengthGateConfig,
	env: GateEnv,
): Promise<number> {
	try {
		const selectedConfig = withSkippedClasses(config, env.skipClasses, env.all);
		if (!env.all && env.event !== "pull_request") {
			deps.log(`length-gate: skipping event ${env.event}`);
			return 0;
		}
		if (
			!env.all &&
			env.event === "pull_request" &&
			env.base === undefined &&
			env.fetch === undefined
		)
			throw new Error(
				"pull_request requires LENGTH_GATE_BASE or LENGTH_GATE_FETCH",
			);

		if (env.all) {
			const head = await deps.resolve(env.head);
			if (head === null)
				throw new Error(
					`head revision ${JSON.stringify(env.head)} does not resolve`,
				);
			const findings: Finding[] = [];
			for (const path of await deps.listFiles(head)) {
				if (isExcluded(path, selectedConfig)) continue;
				const text = await deps.readAt(head, path);
				if (text === null)
					throw new Error(
						`cannot read changed file ${JSON.stringify(path)} at head`,
					);
				const result = judgeFile(selectedConfig, allAddedFile(path, text), {
					before: "",
					after: text,
				});
				findings.push(...result.findings);
				reportNotes(result.notes, deps.log);
			}
			if (findings.length > 0) {
				reportFindings(selectedConfig, findings, deps.err);
				return 1;
			}
			deps.log("length-gate: clean");
			return 0;
		}

		let baseRevision: string;
		let headRevision: string;
		if (env.merge) {
			const head = await deps.resolve(env.head);
			if (head === null)
				throw new Error(
					`head revision ${JSON.stringify(env.head)} does not resolve`,
				);
			const secondParent = await deps.resolve(`${head}^2`);
			if (secondParent === null)
				throw new Error(
					"merge mode requires a head with at least two parents; no second parent found",
				);
			const firstParent = await deps.resolve(`${head}^1`);
			if (firstParent === null)
				throw new Error("merge mode head has no first parent");
			baseRevision = firstParent;
			headRevision = head;
		} else {
			if (env.base !== undefined) {
				const base = await deps.resolve(env.base);
				if (base === null)
					throw new Error(
						`base revision ${JSON.stringify(env.base)} does not resolve`,
					);
				baseRevision = base;
			} else {
				if (env.fetch === undefined)
					throw new Error(
						"pull_request requires LENGTH_GATE_BASE or LENGTH_GATE_FETCH",
					);
				baseRevision = await deps.fetchBase(env.fetch);
			}
			const head = await deps.resolve(env.head);
			if (head === null)
				throw new Error(
					`head revision ${JSON.stringify(env.head)} does not resolve`,
				);
			headRevision = head;
		}

		const mergeBase = await deps.mergeBase(baseRevision, headRevision);
		const rawDiff = await deps.diff(mergeBase, headRevision);
		if (rawDiff.trim() === "") {
			deps.log("length-gate: nothing changed");
			return 0;
		}
		const findings: Finding[] = [];
		for (const file of parseDiff(rawDiff)) {
			if (file.deleted || isExcluded(file.path, selectedConfig)) continue;
			const beforePath = file.oldPath || file.path;
			const afterPath = file.newPath || file.path;
			const before = file.added ? "" : await deps.readAt(mergeBase, beforePath);
			if (before === null)
				throw new Error(
					`cannot read changed file ${JSON.stringify(beforePath)} at merge base`,
				);
			const after = await deps.readAt(headRevision, afterPath);
			if (after === null)
				throw new Error(
					`cannot read changed file ${JSON.stringify(afterPath)} at head`,
				);
			const result = judgeFile(selectedConfig, file, { before, after });
			findings.push(...result.findings);
			reportNotes(result.notes, deps.log);
		}
		if (findings.length > 0) {
			reportFindings(selectedConfig, findings, deps.err);
			return 1;
		}
		deps.log("length-gate: clean");
		return 0;
	} catch (error) {
		deps.err(`length-gate: ${errorMessage(error)}`);
		return 2;
	}
}
