#!/usr/bin/env bun
import { resolve } from "node:path";
import {
	assertNoRouteCollisions,
	collectPages,
	findBrokenLinks,
	readPublishConfig,
} from "./index.ts";
import type {
	BrokenLink,
	BundlePage,
	ConfigMode,
	FetchedSource,
	PublishConfig,
} from "./types.ts";

const USAGE = "Usage: docs-bundle check [--root .] [--config required|default]";

type CliArgs = { root: string; config: ConfigMode };

function parseArgs(args: readonly string[]): CliArgs {
	if (args[0] !== "check") throw new Error("expected the check subcommand");
	let root: string | undefined;
	let config: ConfigMode | undefined;
	for (let index = 1; index < args.length; index += 1) {
		const arg = args[index];
		const value = args[index + 1];
		if (arg !== "--root" && arg !== "--config")
			throw new Error(`unknown argument ${JSON.stringify(arg)}`);
		if (value === undefined || value.length === 0 || value.startsWith("--"))
			throw new Error(`${arg} requires a value`);
		if (arg === "--root") {
			if (root !== undefined) throw new Error("--root may appear once");
			root = value;
		} else {
			if (config !== undefined) throw new Error("--config may appear once");
			if (value !== "required" && value !== "default")
				throw new Error("--config must be required or default");
			config = value;
		}
		index += 1;
	}
	return { root: resolve(root ?? "."), config: config ?? "required" };
}

async function check(root: string, mode: ConfigMode): Promise<number> {
	let config: PublishConfig;
	try {
		config = await readPublishConfig(root, mode);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`docs-bundle: configuration error: ${message}`);
		return 2;
	}

	const source: FetchedSource = {
		repo: "local/worktree",
		mount: "",
		config: mode,
		root,
		sha: "",
	};
	let pages: BundlePage[];
	try {
		pages = await collectPages(source, config);
		assertNoRouteCollisions(pages);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		const collision = /^(\d+) page\(s\) (.+)$/.exec(message);
		if (collision?.[1] !== undefined && collision[2] !== undefined) {
			console.error(`docs-bundle: ${collision[1]} page(s) ${collision[2]}`);
		} else {
			console.error(`docs-bundle: 1 page(s) ${message}`);
		}
		return 1;
	}

	let broken: BrokenLink[];
	try {
		broken = await findBrokenLinks(root, source.repo, pages);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`docs-bundle: page read error: ${message}`);
		return 2;
	}
	if (broken.length > 0) {
		console.error(`docs-bundle: ${broken.length} broken link(s)`);
		for (const link of broken)
			console.error(`  ${link.sourcePath} -> ${link.target}`);
		return 1;
	}
	console.log(`docs-bundle: checked ${pages.length} page(s); no broken links`);
	return 0;
}

async function main(args: readonly string[]): Promise<number> {
	let parsed: CliArgs;
	try {
		parsed = parseArgs(args);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`docs-bundle: ${message}\n${USAGE}`);
		return 2;
	}
	return check(parsed.root, parsed.config);
}

if (import.meta.main) process.exitCode = await main(process.argv.slice(2));
