#!/usr/bin/env bun
import { readFile } from "node:fs/promises";
import { posix as pathPosix, resolve } from "node:path";
import {
	type LedgerConfig,
	loadLedgerConfig,
} from "@rigelbuild/design-ledger-gate";
import {
	type DecisionRow,
	parseDecisionFile,
} from "@rigelbuild/design-ledger-gate/decision-files.ts";

const STATUSES = ["active", "superseded", "retired"] as const;
type Status = (typeof STATUSES)[number];
const VALUE_FLAGS = [
	"--config",
	"--surface",
	"--area",
	"--status",
	"--grep",
	"--glob",
];

export interface Filters {
	surface?: string;
	area?: string;
	status?: Status;
	grep?: string;
}

export interface DecisionFile {
	path: string;
	text: string;
}

export interface ParsedArgs {
	filters: Filters;
	configPath: string;
	glob?: string;
	help: boolean;
}

export interface Output {
	out: (line: string) => void;
	err: (line: string) => void;
}

export const USAGE = `usage: dl-list [--config PATH] [--surface S] [--area A] [--status ${STATUSES.join("|")}] [--grep TEXT] [--glob GLOB]`;

function location(
	path: string,
	surfaceDepth: LedgerConfig["surfaceDepth"],
): string {
	const parts = path.split("/");
	const decisionsIndex = parts.lastIndexOf("decisions");
	const area = parts[decisionsIndex + 1] ?? "";
	if (surfaceDepth === 0) return area;
	const surface = parts[decisionsIndex - 1] ?? "";
	return `${surface}/${area}`;
}

function format(
	row: DecisionRow,
	surfaceDepth: LedgerConfig["surfaceDepth"],
): string {
	const status = row.status.replace(/ \(.+\)$/, "");
	const anchor = row.recordAnchor === null ? "" : `#${row.recordAnchor}`;
	return `${row.id}  ${status}  ${location(row.path, surfaceDepth)}  ${row.decision}  → ${row.recordPath}${anchor}`;
}

function matches(
	row: DecisionRow,
	line: string,
	filters: Filters,
	layout: Pick<LedgerConfig, "surfaceDepth" | "surfaces">,
): boolean {
	const where = location(row.path, layout.surfaceDepth);
	const surface =
		layout.surfaceDepth === 0
			? layout.surfaces?.length === 1
				? layout.surfaces[0]
				: undefined
			: where.split("/")[0];
	if (filters.surface !== undefined && surface !== filters.surface)
		return false;
	if (filters.area !== undefined && where.split("/").at(-1) !== filters.area)
		return false;
	if (
		filters.status !== undefined &&
		!row.status.toLowerCase().startsWith(filters.status)
	)
		return false;
	if (
		filters.grep !== undefined &&
		!line.toLowerCase().includes(filters.grep.toLowerCase())
	)
		return false;
	return true;
}

/** Parse, filter and format decision files; malformed files come back as errors. */
export function listDecisions(
	files: readonly DecisionFile[],
	filters: Filters,
	layout: Pick<LedgerConfig, "surfaceDepth" | "surfaces"> = { surfaceDepth: 1 },
): { lines: string[]; errors: string[] } {
	const rows: DecisionRow[] = [];
	const errors: string[] = [];
	for (const file of files) {
		const result = parseDecisionFile(file.path, file.text);
		if (result.ok) rows.push(result.row);
		else
			errors.push(
				`${result.error.path}:${result.error.line}: ${result.error.reason}`,
			);
	}
	const lines = rows
		.sort((left, right) => Number(left.id.slice(3)) - Number(right.id.slice(3)))
		.map((row) => ({ row, line: format(row, layout.surfaceDepth) }))
		.filter(({ row, line }) => matches(row, line, filters, layout))
		.map(({ line }) => line);
	return { lines, errors };
}

function isStatus(value: string): value is Status {
	return STATUSES.some((status) => status === value);
}

function readArgument(
	argv: readonly string[],
	index: number,
	flag: string,
	inlineValue: string | undefined,
): { value: string; nextIndex: number } {
	const value = inlineValue ?? argv[index + 1];
	if (
		value === undefined ||
		value === "" ||
		(inlineValue === undefined && value.startsWith("--"))
	)
		throw new Error(`${flag} needs a value`);
	return { value, nextIndex: inlineValue === undefined ? index + 1 : index };
}

export function parseArgs(argv: readonly string[]): ParsedArgs {
	const parsed: ParsedArgs = {
		filters: {},
		configPath: "design-ledger.config.json",
		help: false,
	};
	let configProvided = false;
	for (let index = 0; index < argv.length; index += 1) {
		const arg = argv[index] ?? "";
		if (arg === "--help" || arg === "-h") {
			parsed.help = true;
			continue;
		}
		const equals = arg.indexOf("=");
		const flag = equals === -1 ? arg : arg.slice(0, equals);
		if (!VALUE_FLAGS.includes(flag)) throw new Error(`unknown argument ${arg}`);
		const inlineValue = equals === -1 ? undefined : arg.slice(equals + 1);
		const result = readArgument(argv, index, flag, inlineValue);
		index = result.nextIndex;
		if (flag === "--config") {
			if (configProvided) throw new Error("--config may appear once");
			parsed.configPath = result.value;
			configProvided = true;
		} else if (flag === "--glob") parsed.glob = result.value;
		else if (flag === "--status") {
			const status = result.value.toLowerCase();
			if (!isStatus(status))
				throw new Error(`--status must be one of ${STATUSES.join(", ")}`);
			parsed.filters.status = status;
		} else if (flag === "--surface") parsed.filters.surface = result.value;
		else if (flag === "--area") parsed.filters.area = result.value;
		else if (flag === "--grep") parsed.filters.grep = result.value;
	}
	return parsed;
}

function decisionGlob(config: LedgerConfig): string {
	return config.surfaceDepth === 1
		? `${config.designsRoot}/*/decisions/*/DL-*.md`
		: `${config.designsRoot}/decisions/*/DL-*.md`;
}

/** Exit 0 on success, 1 on a malformed file or an empty glob, 2 on a usage/config/read error. */
export async function run(
	argv: readonly string[],
	root: string,
	io: Output,
	readDecisionFile: (path: string) => Promise<string> = (path) =>
		readFile(path, "utf8"),
): Promise<number> {
	let args: ParsedArgs;
	try {
		args = parseArgs(argv);
	} catch (error) {
		io.err(
			`${error instanceof Error ? error.message : String(error)}\n${USAGE}`,
		);
		return 2;
	}
	if (args.help) {
		io.out(USAGE);
		return 0;
	}
	let config: LedgerConfig;
	try {
		config = loadLedgerConfig(resolve(root, args.configPath));
	} catch (error) {
		io.err(
			`dl-list: ${error instanceof Error ? error.message : String(error)}`,
		);
		return 2;
	}
	if (
		config.surfaceDepth === 0 &&
		args.filters.surface !== undefined &&
		config.surfaces?.length !== 1
	) {
		io.err(
			"dl-list: --surface requires exactly one configured surface when surfaceDepth is 0",
		);
		return 2;
	}
	const glob = args.glob ?? decisionGlob(config);
	const files: DecisionFile[] = [];
	try {
		for await (const path of new Bun.Glob(glob).scan({ cwd: root })) {
			const normalized = path.split(pathPosix.sep).join("/");
			files.push({
				path: normalized,
				text: await readDecisionFile(resolve(root, normalized)),
			});
		}
	} catch (error) {
		io.err(
			`dl-list: cannot scan or read decision files matching ${glob}: ${error instanceof Error ? error.message : String(error)}`,
		);
		return 2;
	}
	if (files.length === 0) {
		io.err(`no decision files match ${glob} under ${root}`);
		return 1;
	}
	const { lines, errors } = listDecisions(files, args.filters, config);
	for (const line of lines) io.out(line);
	for (const error of errors) io.err(error);
	return errors.length > 0 ? 1 : 0;
}

if (import.meta.main)
	process.exitCode = await run(Bun.argv.slice(2), process.cwd(), {
		out: (line) => console.log(line),
		err: (line) => console.error(line),
	});
