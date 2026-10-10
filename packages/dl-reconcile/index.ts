#!/usr/bin/env bun
import { readFile } from "node:fs/promises";
import { basename, resolve } from "node:path";
import type { LedgerConfig } from "@rigelbuild/design-ledger-gate";
import {
	classifyDesignPath,
	loadLedgerConfig,
	readDlClaimToken,
	validateDlClaimToken,
} from "@rigelbuild/design-ledger-gate";
import { parseDecisionFile } from "@rigelbuild/design-ledger-gate/decision-files.ts";

export interface LandedDecision {
	id: string;
	surface: string;
	ref: string;
}

export interface ReconcileRequest {
	repo: string;
	landed: LandedDecision[];
}

export interface StaleClaim {
	id: string;
	surface: string;
	ref: string;
	lane: string;
	date: string;
}

export interface DuplicateClaim {
	id: string;
	surfaces: string[];
}

export interface ReconcileResponse {
	updated: number;
	inserted: number;
	stale: StaleClaim[];
	duplicates: DuplicateClaim[];
}

export function assertReconcilableLedgers(
	config: LedgerConfig,
	files: ReadonlyMap<string, string>,
): ReconcileRequest {
	const surfaces = config.surfaces;
	if (surfaces === undefined || surfaces.length === 0)
		throw new Error("config.surfaces must contain at least one surface");
	if (config.surfaceDepth === 0 && surfaces.length !== 1)
		throw new Error(
			"config.surfaces must contain exactly one surface at depth 0",
		);
	const decisions = [...files]
		.filter(([path]) => classifyDesignPath(path, config) === "decision")
		.sort(([left], [right]) => left.localeCompare(right));
	if (decisions.length === 0)
		throw new Error(
			"decision file list is empty; refusing to post an empty frontier",
		);
	const landed: Array<{ path: string; decision: LandedDecision }> = [];
	for (const [path, text] of decisions) {
		const parsed = parseDecisionFile(path, text);
		if (!parsed.ok)
			throw new Error(
				`malformed decision file ${path}: line ${parsed.error.line}: ${parsed.error.reason}`,
			);
		const relative = path.slice(`${config.designsRoot}/`.length).split("/");
		const surface = config.surfaceDepth === 0 ? surfaces[0] : relative[0];
		if (surface === undefined)
			throw new Error(`cannot determine surface for decision file ${path}`);
		if (!surfaces.includes(surface))
			throw new Error(`unknown design surface ${surface} in ${path}`);
		landed.push({
			path,
			decision: { id: basename(path, ".md"), surface, ref: "none" },
		});
	}
	landed.sort(
		(left, right) =>
			Number(left.decision.id.slice(3)) - Number(right.decision.id.slice(3)) ||
			left.path.localeCompare(right.path),
	);
	return {
		repo: config.counter.partition,
		landed: landed.map(({ decision }) => decision),
	};
}

export type FetchFn = (
	input: string | URL | Request,
	init?: RequestInit,
) => Promise<Response>;

export interface ReconcileDeps {
	fetchFn?: FetchFn;
	timeoutMs?: number;
	timeoutSignal?: (timeoutMs: number) => AbortSignal;
}
function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function endpoint(config: LedgerConfig): string {
	return `${config.counter.url.replace(/\/+$/, "")}/reconcile`;
}

export async function reconcile(
	body: ReconcileRequest,
	config: LedgerConfig,
	token: string,
	deps: ReconcileDeps = {},
): Promise<ReconcileResponse> {
	const trimmedToken = validateDlClaimToken(token);
	if (body.landed.length === 0)
		throw new Error("refusing to reconcile an empty landed frontier");
	const timeoutMs =
		deps.timeoutMs !== undefined && deps.timeoutMs > 0
			? deps.timeoutMs
			: 30_000;
	let response: Response;
	try {
		response = await (deps.fetchFn ?? fetch)(endpoint(config), {
			method: "POST",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${trimmedToken}`,
				"Content-Type": "application/json",
			},
			body: JSON.stringify(body),
			signal: (deps.timeoutSignal ?? AbortSignal.timeout)(timeoutMs),
		});
	} catch (error) {
		const name = error instanceof Error ? error.name : "UnknownError";
		throw new Error(`request to ${endpoint(config)} failed (${name})`);
	}
	if (!response.ok)
		throw new Error(`reconciliation failed with HTTP ${response.status}`);
	const payload: unknown = await response.json();
	if (!isRecord(payload)) throw new Error("invalid reconciliation response");
	if (
		typeof payload.updated !== "number" ||
		typeof payload.inserted !== "number" ||
		!Array.isArray(payload.stale) ||
		!Array.isArray(payload.duplicates)
	)
		throw new Error("invalid reconciliation response");
	const staleItems: unknown[] = payload.stale;
	const duplicateItems: unknown[] = payload.duplicates;
	const stale: StaleClaim[] = [];
	for (const item of staleItems) {
		if (
			!isRecord(item) ||
			typeof item.id !== "string" ||
			typeof item.surface !== "string" ||
			typeof item.ref !== "string" ||
			typeof item.lane !== "string" ||
			typeof item.date !== "string"
		)
			throw new Error("invalid reconciliation response");
		stale.push({
			id: item.id,
			surface: item.surface,
			ref: item.ref,
			lane: item.lane,
			date: item.date,
		});
	}
	const duplicates: DuplicateClaim[] = [];
	for (const item of duplicateItems) {
		if (
			!isRecord(item) ||
			typeof item.id !== "string" ||
			!Array.isArray(item.surfaces) ||
			!item.surfaces.every((surface: unknown) => typeof surface === "string")
		)
			throw new Error("invalid reconciliation response");
		const surfaces: string[] = item.surfaces;
		duplicates.push({ id: item.id, surfaces });
	}
	return {
		updated: payload.updated,
		inserted: payload.inserted,
		stale,
		duplicates,
	};
}

export function formatReconcileOutput(response: ReconcileResponse): string {
	const lines = [
		`updated: ${response.updated}  inserted: ${response.inserted}`,
	];
	lines.push(`stale: ${response.stale.length}`);
	for (const entry of response.stale)
		lines.push(
			`  ${entry.id} ${entry.surface} ${entry.lane} ${entry.ref} ${entry.date}`,
		);
	lines.push(`duplicates: ${response.duplicates.length}`);
	for (const entry of response.duplicates)
		lines.push(`  ${entry.id} ${entry.surfaces.join(" ")}`);
	return lines.join("\n");
}

interface CliOptions {
	configPath: string;
	check: boolean;
	staleExit: boolean;
}

export interface RunDeps {
	root?: string;
	token?: string;
	fetchFn?: FetchFn;
	log?: (message: string) => void;
	err?: (message: string) => void;
}

function parseCliArgs(args: readonly string[]): CliOptions {
	let configPath: string | undefined;
	let check = false;
	let staleExit = false;
	for (let index = 0; index < args.length; index += 1) {
		const arg = args[index] ?? "";
		if (arg === "--check") {
			if (check) throw new Error("duplicate --check flag");
			check = true;
		} else if (arg === "--stale-exit") {
			if (staleExit) throw new Error("duplicate --stale-exit flag");
			staleExit = true;
		} else if (arg === "--config") {
			if (configPath !== undefined) throw new Error("duplicate --config flag");
			configPath = args[index + 1];
			if (configPath === undefined || configPath.startsWith("--"))
				throw new Error("--config requires a path");
			index += 1;
		} else if (arg.startsWith("--config=")) {
			if (configPath !== undefined) throw new Error("duplicate --config flag");
			configPath = arg.slice("--config=".length);
			if (configPath.length === 0) throw new Error("--config requires a path");
		} else {
			throw new Error(`unknown argument: ${arg}`);
		}
	}
	if (configPath === undefined) throw new Error("--config is required");
	return { configPath, check, staleExit };
}

function repositoryRoot(): string {
	if (process.env.GATE_ROOT !== undefined)
		return resolve(process.env.GATE_ROOT);
	const result = Bun.spawnSync({
		cmd: ["git", "rev-parse", "--show-toplevel"],
		stdout: "pipe",
		stderr: "pipe",
	});
	if (result.exitCode !== 0)
		throw new Error(
			new TextDecoder().decode(result.stderr).trim() || "cannot find git root",
		);
	return new TextDecoder().decode(result.stdout).trim();
}

async function discoverDecisionFiles(
	root: string,
	config: LedgerConfig,
): Promise<Map<string, string>> {
	const files = new Map<string, string>();
	const glob = new Bun.Glob(`${config.designsRoot}/**`);
	for await (const path of glob.scan({ cwd: root, onlyFiles: true })) {
		const normalizedPath = path.replaceAll("\\", "/");
		const kind = classifyDesignPath(normalizedPath, config);
		if (kind === "misplaced")
			throw new Error(`misplaced decision path ${normalizedPath}`);
		if (kind !== "decision") continue;
		files.set(
			normalizedPath,
			await readFile(resolve(root, normalizedPath), "utf8"),
		);
	}
	return files;
}

export async function runOnce(
	args: readonly string[],
	deps: RunDeps = {},
): Promise<number> {
	const log = deps.log ?? console.log;
	const err = deps.err ?? console.error;
	if (args.length === 1 && args[0] === "--help") {
		log("Usage: dl-reconcile --config <path> [--check] [--stale-exit]");
		return 0;
	}
	let options: CliOptions;
	try {
		options = parseCliArgs(args);
	} catch (error) {
		err(
			`dl-reconcile: ${error instanceof Error ? error.message : String(error)}`,
		);
		return 2;
	}
	let config: LedgerConfig;
	let body: ReconcileRequest;
	try {
		config = loadLedgerConfig(options.configPath);
		const root =
			deps.root === undefined ? repositoryRoot() : resolve(deps.root);
		body = assertReconcilableLedgers(
			config,
			await discoverDecisionFiles(root, config),
		);
	} catch (error) {
		err(
			`dl-reconcile: ${error instanceof Error ? error.message : String(error)}`,
		);
		return 2;
	}
	if (options.check) {
		log(
			`Design ledger parse check passed (${body.landed.length} decision files).`,
		);
		return 0;
	}
	let token: string;
	try {
		token = validateDlClaimToken(deps.token ?? (await readDlClaimToken()));
	} catch (error) {
		const message =
			error instanceof Error && error.message === "DL_CLAIM_TOKEN is required"
				? error.message
				: "DL_CLAIM_TOKEN must contain printable ASCII characters without whitespace";
		err(`dl-reconcile: ${message}`);
		return 2;
	}
	try {
		const response = await reconcile(body, config, token, {
			...(deps.fetchFn === undefined ? {} : { fetchFn: deps.fetchFn }),
		});
		log(formatReconcileOutput(response));
		return options.staleExit && response.stale.length > 0 ? 1 : 0;
	} catch (error) {
		if (
			error instanceof Error &&
			error.message.startsWith("reconciliation failed with HTTP ")
		) {
			err(`dl-reconcile: ${error.message}`);
		} else {
			const name = error instanceof Error ? error.name : "UnknownError";
			err(`dl-reconcile: request to ${endpoint(config)} failed (${name})`);
		}
		return 1;
	}
}
if (import.meta.main) process.exitCode = await runOnce(process.argv.slice(2));
