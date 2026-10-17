#!/usr/bin/env bun
import { resolve as resolvePath } from "node:path";
import { loadConfig } from "./config.ts";
import type { LengthGateConfig } from "./core.ts";
import { createGitDeps, gitTopLevel } from "./git.ts";
import { type GateEnv, runOnce } from "./run.ts";

export { loadConfig } from "./config.ts";
export * from "./core.ts";
export { createGitDeps } from "./git.ts";
export { type Deps, type GateEnv, runOnce } from "./run.ts";

const USAGE =
	"Usage: length-gate --config <path> [--all] [--skip-class <name>]...";

type CliArgs = { configPath: string; all: boolean; skipClasses: string[] };

function parseArgs(args: readonly string[]): CliArgs {
	let configPath: string | undefined;
	let all = false;
	const skipClasses: string[] = [];
	for (let index = 0; index < args.length; index += 1) {
		const arg = args[index];
		if (arg === "--config") {
			if (configPath !== undefined) throw new Error("--config may appear once");
			const value = args[index + 1];
			if (value === undefined || value.length === 0 || value.startsWith("--"))
				throw new Error("--config requires a path");
			configPath = value;
			index += 1;
		} else if (arg === "--all") {
			if (all) throw new Error("--all may appear once");
			all = true;
		} else if (arg === "--skip-class") {
			const value = args[index + 1];
			if (value === undefined || value.length === 0 || value.startsWith("--"))
				throw new Error("--skip-class requires a class name");
			skipClasses.push(value);
			index += 1;
		} else {
			throw new Error(`unknown argument ${JSON.stringify(arg)}`);
		}
	}
	if (configPath === undefined) throw new Error("--config is required");
	return { configPath, all, skipClasses };
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

async function main(args: readonly string[]): Promise<number> {
	let parsed: CliArgs;
	try {
		parsed = parseArgs(args);
	} catch (error) {
		console.error(`length-gate: ${errorMessage(error)}\n${USAGE}`);
		return 2;
	}
	let config: LengthGateConfig;
	try {
		config = loadConfig(parsed.configPath);
	} catch (error) {
		console.error(`length-gate: ${errorMessage(error)}`);
		return 2;
	}
	try {
		const root = gitTopLevel(
			resolvePath(process.env.GATE_ROOT ?? process.cwd()),
		);
		const env: GateEnv = {
			event: process.env.LENGTH_GATE_EVENT ?? "",
			head: process.env.LENGTH_GATE_HEAD ?? "HEAD",
			...(process.env.LENGTH_GATE_BASE === undefined
				? {}
				: { base: process.env.LENGTH_GATE_BASE }),
			...(process.env.LENGTH_GATE_FETCH === undefined
				? {}
				: { fetch: process.env.LENGTH_GATE_FETCH }),
			...(process.env.LENGTH_GATE_MERGE === "1" ? { merge: true } : {}),
			...(parsed.all ? { all: true } : {}),
			...(parsed.skipClasses.length === 0
				? {}
				: { skipClasses: parsed.skipClasses }),
		};
		return await runOnce(createGitDeps(root), config, env);
	} catch (error) {
		console.error(`length-gate: ${errorMessage(error)}`);
		return 2;
	}
}

if (import.meta.main) process.exitCode = await main(process.argv.slice(2));
