#!/usr/bin/env bun
// Refresh source and vendor hashes after Renovate bumps a Go analysis pin.

import { readFileSync, writeFileSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { $ } from "bun";

export const PIN_FILE = "tools/toolchain/go-analysis-pins.nix";
const FAKE_SRI = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
const SOURCE_MARKER = 'hash = "sha256-';
const VENDOR_MARKER = 'vendorHash = "sha256-';

type ToolEntry = {
	readonly attr: "nilaway" | "golangci-lint";
	readonly sourceAttr: "src";
	readonly vendorAttr: "goModules";
	readonly derived: "version-from-rev-date" | "tag-from-version";
};

const TOOL_ENTRIES: readonly ToolEntry[] = [
	{
		attr: "nilaway",
		sourceAttr: "src",
		vendorAttr: "goModules",
		derived: "version-from-rev-date",
	},
	{
		attr: "golangci-lint",
		sourceAttr: "src",
		vendorAttr: "goModules",
		derived: "tag-from-version",
	},
];

function fail(message: string): never {
	throw new Error(`renovate-go-analysis: ${message}`);
}

function toolBlock(text: string, entry: ToolEntry): string {
	const lines = text.split("\n");
	const start = lines.findIndex((line) =>
		line.trimStart().startsWith(`${entry.attr} = {`),
	);
	const end = lines.findIndex(
		(line, index) => index > start && line.trim() === "};",
	);
	if (start < 0 || end < 0) fail(`missing pin block for ${entry.attr}`);
	return lines.slice(start, end + 1).join("\n");
}

function replaceToolBlock(
	text: string,
	entry: ToolEntry,
	updated: string,
): string {
	const original = toolBlock(text, entry);
	const start = text.indexOf(original);
	if (start < 0) fail(`cannot replace pin block for ${entry.attr}`);
	return `${text.slice(0, start)}${updated}${text.slice(start + original.length)}`;
}

function fieldValue(block: string, field: string, entry: ToolEntry): string {
	const value = block.match(
		new RegExp(`^\\s*${field}\\s*=\\s*"([^"]+)"`, "m"),
	)?.[1];
	if (!value) fail(`missing ${field} in ${entry.attr} block`);
	return value;
}

function rewriteField(
	block: string,
	field: string,
	value: string,
	entry: ToolEntry,
): string {
	const pattern = new RegExp(
		`(^\\s*${field}\\s*=\\s*")[^"]*("\\s*;\\s*$)`,
		"m",
	);
	if (!pattern.test(block)) fail(`missing ${field} in ${entry.attr} block`);
	return block.replace(
		pattern,
		(_line, before: string, after: string) => `${before}${value}${after}`,
	);
}

function rewriteHash(
	block: string,
	marker: string,
	value: string,
	entry: ToolEntry,
): string {
	const lines = block.split("\n");
	const index = lines.findIndex((line) => line.includes(marker));
	if (index < 0) fail(`missing hash marker in ${entry.attr} block`);
	lines[index] = (lines[index] as string).replace(/sha256-[^"]*/, () => value);
	return lines.join("\n");
}

function parseGot(output: string, drvFragment: string): string {
	const lines = output.split("\n");
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] as string;
		if (
			!line.includes("hash mismatch in fixed-output derivation") ||
			!line.includes(drvFragment)
		)
			continue;
		for (let next = index + 1; next < lines.length; next += 1) {
			const candidate = lines[next] as string;
			if (candidate.includes("hash mismatch in fixed-output derivation")) break;
			const hash = candidate.match(/got:\s*(sha256-\S+)/)?.[1];
			if (hash) return hash;
		}
	}
	return fail(`no got hash for ${drvFragment}: ${output}`);
}

async function nilawayVersionDate(rev: string): Promise<string> {
	if (!/^[a-f0-9]{40}$/.test(rev)) fail(`invalid nilaway revision ${rev}`);
	const directory = await mkdtemp(join(tmpdir(), "renovate-go-analysis-"));
	try {
		await $`git init -q`.cwd(directory).quiet();
		const fetched =
			await $`git fetch --depth=1 https://github.com/uber-go/nilaway ${rev}`
				.cwd(directory)
				.nothrow()
				.quiet();
		if (fetched.exitCode !== 0)
			fail(`nilaway revision fetch failed: ${fetched.stderr.toString()}`);
		const logged =
			await $`git log -1 --date=format-local:%Y-%m-%d --format=%cd FETCH_HEAD`
				.cwd(directory)
				.env({ TZ: "UTC" })
				.nothrow()
				.quiet();
		const date = logged.stdout.toString().trim();
		if (logged.exitCode !== 0 || !/^\d{4}-\d{2}-\d{2}$/.test(date))
			fail("nilaway commit date lookup failed");
		return date;
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

async function resolveDrvPath(
	entry: ToolEntry,
	attr: ToolEntry["sourceAttr"] | ToolEntry["vendorAttr"],
): Promise<string> {
	const expression = `let flake = builtins.getFlake (toString ./.); pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux; tools = flake.lib.goAnalysis { inherit pkgs; goToolchain = pkgs.go; }; in tools.${entry.attr}.${attr}.drvPath`;
	const result = await $`nix eval --raw --impure --expr ${expression}`
		.nothrow()
		.quiet();
	if (result.exitCode !== 0)
		fail(
			`could not resolve ${entry.attr}.${attr}: ${result.stderr.toString()}`,
		);
	const drvPath = result.stdout.toString().trim();
	if (!drvPath.endsWith(".drv"))
		fail(`invalid derivation path for ${entry.attr}.${attr}`);
	return basename(drvPath);
}

async function recomputeHash(
	entry: ToolEntry,
	marker: string,
	hashAttr: ToolEntry["sourceAttr"] | ToolEntry["vendorAttr"],
	text: string,
): Promise<string> {
	const block = rewriteHash(toolBlock(text, entry), marker, FAKE_SRI, entry);
	writeFileSync(PIN_FILE, replaceToolBlock(text, entry, block));
	const drvFragment = await resolveDrvPath(entry, hashAttr);
	const result =
		await $`nix build --no-link --keep-going --impure --expr ${`let flake = builtins.getFlake (toString ./.); pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux; tools = flake.lib.goAnalysis { inherit pkgs; goToolchain = pkgs.go; }; in tools.${entry.attr}`}`
			.nothrow()
			.quiet();
	const output = `${result.stdout.toString()}\n${result.stderr.toString()}`;
	if (result.exitCode === 0)
		fail(`fake ${hashAttr} hash unexpectedly built successfully`);
	return parseGot(output, drvFragment.replace(/\.drv$/, ""));
}

async function refreshTool(entry: ToolEntry, text: string): Promise<string> {
	let block = toolBlock(text, entry);
	if (entry.derived === "tag-from-version") {
		block = rewriteField(
			block,
			"tag",
			`v${fieldValue(block, "version", entry)}`,
			entry,
		);
	} else {
		const date = await nilawayVersionDate(fieldValue(block, "rev", entry));
		block = rewriteField(block, "version", `0-unstable-${date}`, entry);
	}
	let updated = replaceToolBlock(text, entry, block);
	writeFileSync(PIN_FILE, updated);
	const sourceHash = await recomputeHash(
		entry,
		SOURCE_MARKER,
		entry.sourceAttr,
		updated,
	);
	block = rewriteHash(
		toolBlock(updated, entry),
		SOURCE_MARKER,
		sourceHash,
		entry,
	);
	updated = replaceToolBlock(updated, entry, block);
	writeFileSync(PIN_FILE, updated);
	const vendorHash = await recomputeHash(
		entry,
		VENDOR_MARKER,
		entry.vendorAttr,
		updated,
	);
	block = rewriteHash(
		toolBlock(updated, entry),
		VENDOR_MARKER,
		vendorHash,
		entry,
	);
	updated = replaceToolBlock(updated, entry, block);
	writeFileSync(PIN_FILE, updated);
	console.log(
		`renovate-go-analysis: refreshed ${entry.attr} source and vendor hashes`,
	);
	return updated;
}

async function main(): Promise<void> {
	const repoRoot = (await $`git rev-parse --show-toplevel`.text()).trim();
	process.chdir(repoRoot);
	const baseBranch = process.env.RENOVATE_BASE_BRANCH || "main";
	const remoteRef = `origin/${baseBranch}`;
	const baseRef =
		(await $`git rev-parse --verify -q ${remoteRef}`.nothrow().quiet())
			.exitCode === 0
			? remoteRef
			: baseBranch;
	const changed = await $`git diff --quiet ${baseRef} -- ${PIN_FILE}`
		.nothrow()
		.quiet();
	if (changed.exitCode === 0) return;

	const original = readFileSync(PIN_FILE, "utf8");
	const base = await $`git show ${baseRef}:${PIN_FILE}`.nothrow().quiet();
	if (base.exitCode !== 0) fail(`could not read ${PIN_FILE} from ${baseRef}`);
	const baseText = base.stdout.toString();
	let current = original;
	const restore = () => writeFileSync(PIN_FILE, original);
	const onSignal = (signal: "SIGINT" | "SIGTERM") => {
		restore();
		console.error(`renovate-go-analysis: interrupted by ${signal}`);
		process.exit(1);
	};
	process.on("SIGINT", onSignal);
	process.on("SIGTERM", onSignal);
	try {
		for (const entry of TOOL_ENTRIES) {
			if (toolBlock(original, entry) === toolBlock(baseText, entry)) continue;
			current = await refreshTool(entry, current);
		}
	} catch (error) {
		restore();
		throw error;
	} finally {
		process.off("SIGINT", onSignal);
		process.off("SIGTERM", onSignal);
	}
}

if (import.meta.main) {
	try {
		await main();
	} catch (error) {
		console.error(error instanceof Error ? error.message : String(error));
		process.exitCode = 1;
	}
}
