#!/usr/bin/env bun
import { existsSync, readFileSync } from "node:fs";
import { posix as pathPosix, resolve } from "node:path";
import { $ } from "bun";
import { type DecisionRow, parseDecisionFile } from "./decision-files.ts";

export interface LedgerConfig {
	readonly designsRoot: string;
	readonly surfaceDepth: 0 | 1;
	readonly governedRoots?: readonly string[];
	/** Absent permits any record; present restricts Historical to these paths. */
	readonly historicalChain?: readonly string[];
	readonly exemptBranchPrefixes?: readonly string[];
	/** "changed" checks record `Status:` lines only in PR-changed files; default "all". */
	readonly recordStatusScope?: "all" | "changed";
	readonly citationAmbiguousPaths?: readonly string[];
	readonly legs?: {
		readonly citations?: boolean;
		readonly errata?: boolean;
		readonly recordLinks?: boolean;
		readonly mainIds?: boolean;
	};
	readonly counter: { readonly url: string; readonly partition: string };
	readonly remediationDoc?: string;
}

function asRecord(value: unknown, label: string): Record<string, unknown> {
	if (typeof value !== "object" || value === null || Array.isArray(value))
		throw new Error(`${label} must be an object`);
	// SAFETY: the value was checked as a non-null, non-array object above.
	return value as Record<string, unknown>;
}

function rejectUnknownKeys(
	value: Record<string, unknown>,
	allowed: readonly string[],
	label: string,
): void {
	for (const key of Object.keys(value))
		if (!allowed.includes(key))
			throw new Error(`Unknown key "${key}" in ${label}`);
}

function readStringArray(value: unknown, label: string): string[] {
	if (!Array.isArray(value))
		throw new Error(`${label} must be an array of strings`);
	const result: string[] = [];
	for (const [index, item] of value.entries()) {
		if (typeof item !== "string")
			throw new Error(`${label}[${index}] must be a string`);
		result.push(item);
	}
	return result;
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

export function loadLedgerConfig(path: string): LedgerConfig {
	let parsed: unknown;
	try {
		parsed = JSON.parse(readFileSync(path, "utf8"));
	} catch (error) {
		throw new Error(`Cannot load config "${path}": ${errorMessage(error)}`, {
			cause: error,
		});
	}
	const raw = asRecord(parsed, "Config");
	rejectUnknownKeys(
		raw,
		[
			"designsRoot",
			"surfaceDepth",
			"governedRoots",
			"historicalChain",
			"exemptBranchPrefixes",
			"citationAmbiguousPaths",
			"recordStatusScope",
			"legs",
			"counter",
			"remediationDoc",
		],
		"config",
	);
	if (typeof raw.designsRoot !== "string" || raw.designsRoot === "")
		throw new Error("designsRoot must be a non-empty string");
	if (raw.surfaceDepth !== 0 && raw.surfaceDepth !== 1)
		throw new Error("surfaceDepth must be 0 or 1");
	let governedRoots: string[] | undefined;
	if (raw.governedRoots !== undefined)
		governedRoots = readStringArray(raw.governedRoots, "governedRoots");
	if (raw.surfaceDepth === 0 && governedRoots === undefined)
		throw new Error("governedRoots is required when surfaceDepth is 0");
	if (raw.surfaceDepth === 1 && governedRoots !== undefined)
		throw new Error("governedRoots is only valid when surfaceDepth is 0");
	let historicalChain: string[] | undefined;
	if (raw.historicalChain !== undefined)
		historicalChain = readStringArray(raw.historicalChain, "historicalChain");
	let exemptBranchPrefixes: string[] | undefined;
	if (raw.exemptBranchPrefixes !== undefined)
		exemptBranchPrefixes = readStringArray(
			raw.exemptBranchPrefixes,
			"exemptBranchPrefixes",
		);
	const citationAmbiguousPaths =
		raw.citationAmbiguousPaths === undefined
			? []
			: readStringArray(raw.citationAmbiguousPaths, "citationAmbiguousPaths");
	if (
		citationAmbiguousPaths?.some(
			(item) =>
				item === "" ||
				item === "." ||
				item === ".." ||
				item.includes("\\") ||
				pathPosix.isAbsolute(item) ||
				pathPosix.normalize(item) !== item ||
				item.startsWith("../"),
		) === true
	)
		throw new Error(
			"citationAmbiguousPaths entries must be normalized repository-relative paths",
		);
	const scope = raw.recordStatusScope;
	if (scope !== undefined && scope !== "all" && scope !== "changed")
		throw new Error('recordStatusScope must be "all" or "changed"');
	let legs: LedgerConfig["legs"];
	if (raw.legs !== undefined) {
		const legConfig = asRecord(raw.legs, "legs");
		rejectUnknownKeys(
			legConfig,
			["citations", "errata", "recordLinks", "mainIds"],
			"legs",
		);
		for (const [key, value] of Object.entries(legConfig))
			if (typeof value !== "boolean")
				throw new Error(`legs.${key} must be a boolean`);
		legs = {
			citations:
				typeof legConfig.citations === "boolean"
					? legConfig.citations
					: undefined,
			errata:
				typeof legConfig.errata === "boolean" ? legConfig.errata : undefined,
			recordLinks:
				typeof legConfig.recordLinks === "boolean"
					? legConfig.recordLinks
					: undefined,
			mainIds:
				typeof legConfig.mainIds === "boolean" ? legConfig.mainIds : undefined,
		};
	}
	const counter = asRecord(raw.counter, "counter");
	rejectUnknownKeys(counter, ["url", "partition"], "counter");
	if (typeof counter.url !== "string" || typeof counter.partition !== "string")
		throw new Error("counter.url and counter.partition must be strings");
	if (
		raw.remediationDoc !== undefined &&
		typeof raw.remediationDoc !== "string"
	)
		throw new Error("remediationDoc must be a string");
	return {
		designsRoot: raw.designsRoot,
		surfaceDepth: raw.surfaceDepth,
		...(governedRoots === undefined ? {} : { governedRoots }),
		...(historicalChain === undefined ? {} : { historicalChain }),
		...(exemptBranchPrefixes === undefined ? {} : { exemptBranchPrefixes }),
		citationAmbiguousPaths,
		...(scope === undefined ? {} : { recordStatusScope: scope }),
		...(legs === undefined ? {} : { legs }),
		counter: { url: counter.url, partition: counter.partition },
		...(typeof raw.remediationDoc === "string"
			? { remediationDoc: raw.remediationDoc }
			: {}),
	};
}

export const LARGE_RECORD_BYTES = 50 * 1024;
export const KEY_LINE = { id: 2, decision: 3, status: 4, record: 5 } as const;
export type DesignPathKind =
	| "decision"
	| "decisions-readme"
	| "legacy-ledger"
	| "misplaced"
	| "other";
export interface StrayPath {
	path: string;
	kind: "legacy-ledger" | "misplaced";
}
export interface DecisionCorpus {
	rows: DecisionRow[];
	malformed: Array<{ path: string; line: number; reason: string }>;
	strays: StrayPath[];
}
export interface RecordHeader {
	path: string;
	statusLine: string | null;
	line: number;
}
export interface Changed {
	files: string[];
	body: string | null;
	headBranch: string;
}
export interface RecordContent {
	headings: string[];
	sizeBytes: number;
}
export interface Violation {
	file: string;
	line: number;
	message: string;
}
export type StatusValue =
	| { kind: "Historical" }
	| { kind: "Superseded"; path: string };

const LEDGER_IMPACT_RE = /^\s*>?\s*ledger-impact:\s*(\S.*)$/im;
const STATUS_RE =
	/^\s*>?\s*(?:\*\*)?status:(?:\*\*)?\s*(?:\*\*)?(superseded by (\S+)|historical)(?:\*\*)?(?:[\s.,;:()—-].*)?$/i;
const ROW_SUPERSEDED_RE =
	/^Superseded by (DL-(?:\d{3}|[1-9]\d{3,})) \(.+, \d{4}-\d{2}-\d{2}\)$/;

export function classifyDesignPath(
	file: string,
	config: LedgerConfig,
): DesignPathKind {
	const root = `${config.designsRoot}/`;
	if (!file.startsWith(root)) return "other";
	const base = pathPosix.basename(file);
	if (base === "DECISIONS.md") return "legacy-ledger";
	const relative = file.slice(root.length).split("/");
	const decisionIndex = config.surfaceDepth === 0 ? 0 : 1;
	if (relative[decisionIndex] === "decisions") {
		const decisionPath = relative.slice(decisionIndex + 1);
		if (decisionPath.length === 1 && decisionPath[0] === "README.md")
			return "decisions-readme";
		return decisionPath.length === 2 &&
			decisionPath[0] !== "" &&
			/^DL-.*\.md$/.test(base)
			? "decision"
			: "misplaced";
	}
	if (
		config.surfaceDepth === 1 &&
		relative.length >= 2 &&
		relative[1] === "DECISIONS.md"
	)
		return "legacy-ledger";
	return /^DL-\d+\.md$/.test(base) ? "misplaced" : "other";
}

export function buildDecisionCorpus(
	files: readonly { path: string; text: string }[],
	strays: readonly StrayPath[] = [],
): DecisionCorpus {
	const rows: DecisionRow[] = [];
	const malformed: DecisionCorpus["malformed"] = [];
	for (const file of files) {
		const parsed = parseDecisionFile(file.path, file.text);
		if (parsed.ok) rows.push(parsed.row);
		else malformed.push(parsed.error);
	}
	return { rows, malformed, strays: [...strays] };
}

export function slugify(heading: string): string {
	return heading
		.trim()
		.toLowerCase()
		.replace(/[^\w\s-]/gu, "")
		.replace(/\s/g, "-");
}

export function parseStatusValue(statusLine: string): StatusValue | null {
	const match = STATUS_RE.exec(statusLine);
	if (match === null) return null;
	if (match[1]?.toLowerCase() === "historical") return { kind: "Historical" };
	const path = match[2]?.replace(/[.,;:()—*-]+$/u, "");
	return path === undefined || path === ""
		? null
		: { kind: "Superseded", path };
}

export function touchesRecord(file: string, config: LedgerConfig): boolean {
	if (!file.endsWith(".md")) return false;
	const root = `${config.designsRoot}/`;
	if (!file.startsWith(root)) return false;
	const relative = file.slice(root.length).split("/");
	if (relative[0] === "decisions") return false;
	if (config.surfaceDepth === 0)
		return (config.governedRoots ?? []).some((area) => relative[0] === area);
	return relative.length >= 2 && relative[1] !== "decisions";
}

export function resolveRecordRelative(
	recordRelPath: string,
	pointer: string,
): string | null {
	const joined = pathPosix.join(pathPosix.dirname(recordRelPath), pointer);
	return joined.startsWith("..") ? null : joined;
}

export function conflictMarkerViolations(
	file: string,
	text: string,
): Violation[] {
	const out: Violation[] = [];
	let inFence = false;
	text.split("\n").forEach((line, index) => {
		if (/^\s*(```|~~~)/.test(line)) inFence = !inFence;
		if (inFence) return;
		const marker = /^(<{7,}|>{7,}|%{7,}|\+{7,}|={7})(\s|$)/.exec(line);
		if (marker !== null)
			out.push({
				file,
				line: index + 1,
				message: `unresolved merge conflict marker: ${marker[1]}`,
			});
	});
	return out;
}

export function parseRecordHeader(path: string, text: string): RecordHeader {
	const lines = text.split("\n");
	let h1 = -1;
	let inFence = false;
	for (let index = 0; index < lines.length; index++) {
		const line = lines[index] ?? "";
		if (/^\s*(```|~~~)/.test(line)) {
			inFence = !inFence;
			continue;
		}
		if (!inFence && /^#\s/.test(line)) {
			h1 = index;
			break;
		}
	}
	if (h1 === -1) return { path, statusLine: null, line: 1 };
	inFence = false;
	for (let index = h1 + 1; index < lines.length; index++) {
		const raw = lines[index] ?? "";
		if (/^\s*(```|~~~)/.test(raw)) {
			inFence = !inFence;
			continue;
		}
		if (inFence) continue;
		if (/^##\s/.test(raw)) break;
		if (/^\s*(?:>\s*)*(?:\*\*)?Status:/i.test(raw))
			return { path, statusLine: raw.trimEnd(), line: index + 1 };
	}
	return { path, statusLine: null, line: h1 + 2 };
}

export function recordContentFromText(text: string): RecordContent {
	const headings: string[] = [];
	let inFence = false;
	for (const line of text.split("\n")) {
		if (/^\s*(```|~~~)/.test(line)) {
			inFence = !inFence;
			continue;
		}
		if (inFence) continue;
		const heading = /^#{1,6}\s+(.*)$/.exec(line);
		if (heading !== null) headings.push(slugify(heading[1] ?? ""));
	}
	return { headings, sizeBytes: Buffer.byteLength(text, "utf8") };
}

export function evaluate(
	corpus: DecisionCorpus,
	records: RecordHeader[],
	changed: Changed,
	readRecord: (path: string) => RecordContent | null,
	config: LedgerConfig,
): Violation[] {
	const violations: Violation[] = [];
	const v = (file: string, line: number, message: string) =>
		violations.push({ file, line, message });
	const { rows } = corpus;
	for (const stray of corpus.strays)
		v(
			stray.path,
			0,
			stray.kind === "legacy-ledger"
				? "DECISIONS.md is retired: decision files belong in the configured decisions tree"
				: "misplaced design file: decision files must follow the configured layout",
		);
	for (const malformed of corpus.malformed)
		v(
			malformed.path,
			malformed.line,
			`malformed decision file: ${malformed.reason}`,
		);
	if (rows.length === 0)
		v(
			`${config.designsRoot}/decisions`,
			0,
			"no valid decision files were found",
		);
	const byId = new Map<string, DecisionRow>();
	for (const row of rows) {
		const first = byId.get(row.id);
		if (first === undefined) byId.set(row.id, row);
		else
			v(
				row.path,
				KEY_LINE.id,
				`${row.id}: duplicate decision id (also defined in ${first.path})`,
			);
	}
	for (const row of rows) {
		const decisionParts = row.path
			.slice(config.designsRoot.length + 1)
			.split("/");
		const recordParts = row.recordPath.startsWith(`${config.designsRoot}/`)
			? row.recordPath.slice(config.designsRoot.length + 1).split("/")
			: [];
		const decisionArea = decisionParts[1] ?? "";
		const recordArea = recordParts[0] ?? "";
		const areaMismatch =
			!row.recordPath.startsWith(`${config.designsRoot}/`) ||
			(config.surfaceDepth === 0 &&
				(recordArea === "" || recordArea !== decisionArea));
		if (areaMismatch)
			v(
				row.path,
				KEY_LINE.record,
				`${row.id}: decision area must match its Record path (${row.recordPath})`,
			);
		const target = readRecord(row.recordPath);
		if (target === null)
			v(
				row.path,
				KEY_LINE.record,
				`${row.id}: Record link path does not resolve: ${row.recordRaw}`,
			);
		else if (
			row.recordAnchor !== null &&
			!target.headings.includes(row.recordAnchor)
		)
			v(
				row.path,
				KEY_LINE.record,
				`${row.id}: Record link #anchor not found in ${row.recordRaw.split("#")[0]}: #${row.recordAnchor}`,
			);
		else if (row.recordAnchor === null && target.sizeBytes > LARGE_RECORD_BYTES)
			v(
				row.path,
				KEY_LINE.record,
				`${row.id}: Record link into a large record must carry a #anchor: ${row.recordRaw}`,
			);
		const supersession = ROW_SUPERSEDED_RE.exec(row.status);
		if (supersession !== null) {
			const targetId = supersession[1] ?? "";
			if (targetId === row.id)
				v(row.path, KEY_LINE.status, `${row.id}: superseded by itself`);
			else if (!byId.has(targetId))
				v(
					row.path,
					KEY_LINE.status,
					`${row.id}: Superseded by ${targetId}, which is not a decision file`,
				);
		}
	}
	const cyclesReported = new Set<string>();
	for (const start of byId.values()) {
		if (!ROW_SUPERSEDED_RE.test(start.status)) continue;
		const walk: DecisionRow[] = [];
		let current: DecisionRow | undefined = start;
		while (current !== undefined) {
			const node: DecisionRow = current;
			const index = walk.findIndex((candidate) => candidate.id === node.id);
			if (index !== -1) {
				const cycle = walk.slice(index);
				const key = cycle
					.map((row) => row.id)
					.sort()
					.join("|");
				if (cycle.length > 1 && !cyclesReported.has(key)) {
					cyclesReported.add(key);
					const anchor = cycle.reduce((a, b) => (b.id < a.id ? b : a));
					v(
						anchor.path,
						KEY_LINE.status,
						`supersession cycle: ${cycle.map((row) => row.id).join(" → ")} → ${node.id}`,
					);
				}
				break;
			}
			walk.push(node);
			const nextId: string | undefined = ROW_SUPERSEDED_RE.exec(
				node.status,
			)?.[1];
			current = nextId === undefined ? undefined : byId.get(nextId);
		}
	}
	const decisionAreas = new Set(
		rows.map((row) =>
			row.path
				.slice(config.designsRoot.length + 1)
				.split("/")
				.slice(config.surfaceDepth + 1, config.surfaceDepth + 2)
				.join("/"),
		),
	);
	if (config.surfaceDepth === 0) {
		for (const area of config.governedRoots ?? []) {
			const hasRecords = records.some(
				(record) =>
					touchesRecord(record.path, config) &&
					record.path.startsWith(`${config.designsRoot}/${area}/`),
			);
			if (hasRecords && !decisionAreas.has(area))
				v(
					`${config.designsRoot}/decisions/${area}`,
					0,
					`${config.designsRoot}/${area}/ has records but no valid decision file in decisions/${area}/`,
				);
		}
	} else {
		const decisionSurfaces = new Set(
			rows.map(
				(row) =>
					row.path.slice(config.designsRoot.length + 1).split("/")[0] ?? "",
			),
		);
		const recordSurfaces = new Set(
			records
				.filter((record) => touchesRecord(record.path, config))
				.map(
					(record) =>
						record.path.slice(config.designsRoot.length + 1).split("/")[0] ??
						"",
				),
		);
		for (const surface of recordSurfaces)
			if (!decisionSurfaces.has(surface))
				v(
					`${config.designsRoot}/${surface}/decisions`,
					0,
					`${surface} has records but no valid decision file`,
				);
	}
	const rowByRecord = new Map<string, DecisionRow>();
	for (const row of rows)
		if (row.recordAnchor === null) rowByRecord.set(row.recordPath, row);
	const changedFiles = new Set(changed.files);
	for (const record of records) {
		if (record.statusLine === null) continue;
		if (
			config.recordStatusScope === "changed" &&
			!changedFiles.has(record.path)
		)
			continue;
		const value = parseStatusValue(record.statusLine);
		if (value === null) {
			v(record.path, record.line, "malformed or prohibited `Status:` header");
			continue;
		}
		if (
			value.kind === "Historical" &&
			config.historicalChain !== undefined &&
			!config.historicalChain.includes(record.path)
		)
			v(
				record.path,
				record.line,
				"`Status: Historical` but the record is not in the configured historical chain",
			);
		if (value.kind === "Superseded") {
			const recordRelative = record.path.startsWith(`${config.designsRoot}/`)
				? record.path.slice(config.designsRoot.length + 1)
				: record.path;
			const resolved = resolveRecordRelative(recordRelative, value.path);
			const targetPath =
				resolved === null ? null : `${config.designsRoot}/${resolved}`;
			if (targetPath === null || readRecord(targetPath) === null)
				v(
					record.path,
					record.line,
					`Status supersession does not resolve to a record: ${value.path}`,
				);
			const linked = rowByRecord.get(record.path);
			if (linked !== undefined && !ROW_SUPERSEDED_RE.test(linked.status))
				v(
					record.path,
					record.line,
					"record Status supersession disagrees with its decision status",
				);
		}
	}
	const exempt = (
		config.exemptBranchPrefixes ?? ["renovate/", "trunk-merge/"]
	).some((prefix) => changed.headBranch.startsWith(prefix));
	const declared = LEDGER_IMPACT_RE.test(changed.body ?? "");
	const touchedRecord =
		!exempt && changed.files.some((file) => touchesRecord(file, config));
	const touchedDecision = changed.files.some(
		(file) => classifyDesignPath(file, config) === "decision",
	);
	if (touchedRecord && !touchedDecision && !declared)
		v(
			"(pull request)",
			0,
			"PR touches a governed design record without a changed decision file or Ledger-impact declaration",
		);
	return violations;
}

export interface Deps {
	readonly root: string;
	readonly readText: (root: string, path: string) => Promise<string | null>;
	readonly listDesignFiles: (
		root: string,
		designsRoot: string,
	) => Promise<string[]>;
	readonly readRecord: (root: string, path: string) => RecordContent | null;
	readonly readBaseDecisionPaths: (
		root: string,
		baseRef: string,
	) => Promise<readonly string[]>;
	readonly changed: Changed;
	readonly log: (message: string) => void;
	readonly err: (message: string) => void;
}

const CITATION_RE =
	/`([^`\s]*?\.md):(\d+(?:-\d+)?(?:\s*,\s*\d+(?:-\d+)?)*)[^`]*`/g;
const FENCE_OPEN_RE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const FENCE_CLOSE_RE = /^ {0,3}(`+|~+)[ \t]*$/;

interface MarkdownLine {
	readonly line: number;
	readonly text: string;
}

interface MarkdownScan {
	readonly lines: MarkdownLine[];
	readonly openFenceLine: number | null;
}

function markdownLinesOutsideFences(text: string): MarkdownScan {
	const visible: MarkdownLine[] = [];
	let fence: { char: "`" | "~"; length: number; line: number } | undefined;
	for (const [index, raw] of text.split("\n").entries()) {
		const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
		if (fence !== undefined) {
			const closing = FENCE_CLOSE_RE.exec(line)?.[1];
			if (
				closing !== undefined &&
				closing[0] === fence.char &&
				closing.length >= fence.length
			)
				fence = undefined;
			continue;
		}
		const opener = FENCE_OPEN_RE.exec(line);
		const marker = opener?.[1];
		if (marker !== undefined) {
			if (marker[0] === "`" && (opener?.[2] ?? "").includes("`")) {
				visible.push({ line: index + 1, text: line });
				continue;
			}
			fence = {
				char: marker[0] === "`" ? "`" : "~",
				length: marker.length,
				line: index + 1,
			};
			continue;
		}
		visible.push({ line: index + 1, text: line });
	}
	return { lines: visible, openFenceLine: fence?.line ?? null };
}

function headingSlugs(text: string): Set<string> {
	const slugs = new Set<string>();
	for (const { text: line } of markdownLinesOutsideFences(text).lines) {
		const heading = /^#{1,6}\s+(.+?)\s*#*\s*$/.exec(line);
		if (heading?.[1] !== undefined) slugs.add(slugify(heading[1]));
	}
	return slugs;
}

function withinRepo(root: string, file: string): boolean {
	const absolute = resolve(root, file);
	const repo = resolve(root);
	const relative = pathPosix.relative(
		repo.replaceAll("\\", "/"),
		absolute.replaceAll("\\", "/"),
	);
	return (
		relative !== ".." &&
		!relative.startsWith("../") &&
		!pathPosix.isAbsolute(relative)
	);
}

function validateErrata(path: string, text: string): Violation[] {
	const lines = text.split("\n");
	const scan = markdownLinesOutsideFences(text);
	const visible = scan.lines;
	const errataHeading = visible.find((item) =>
		/^## Errata\s*$/.test(item.text),
	);
	const headingIndex =
		errataHeading === undefined ? -1 : errataHeading.line - 1;
	const h1Index = visible.find((item) => /^#\s/.test(item.text))?.line ?? 0;
	const h2Indexes = visible
		.filter((item) => /^##\s/.test(item.text))
		.map((item) => item.line - 1);
	const finalH2 = h2Indexes.at(-1) === headingIndex;
	const firstH2 = h2Indexes[0] ?? lines.length;
	const headerLine = visible.find(
		(item) => item.line - 1 < firstH2 && /^\s*Errata\s*:/i.test(item.text),
	);
	if (headingIndex === -1) {
		return headerLine === undefined
			? []
			: [
					{
						file: path,
						line: headerLine.line,
						message: "Errata marker requires an Errata section",
					},
				];
	}
	const out: Violation[] = [];
	if (!finalH2)
		out.push({
			file: path,
			line: headingIndex + 1,
			message: "Errata must be the final H2",
		});
	const entries: Array<{ id: number; headingLine: number; quote?: string }> =
		[];
	let current: (typeof entries)[number] | undefined;
	let malformedEntries = false;
	for (const item of visible) {
		if (item.line <= headingIndex + 1) continue;
		if (/^##\s/.test(item.text)) break;
		if (/^#{1,2}\s/.test(item.text)) {
			current = undefined;
			continue;
		}
		if (/^###\s+E\d/.test(item.text)) {
			const heading =
				/^###\s+E(\d+)\s+—\s+\d{4}-\d{2}-\d{2}\s+\([^)]+\)\s*$/.exec(item.text);
			if (heading === null) {
				out.push({
					file: path,
					line: item.line,
					message: "malformed errata entry heading",
				});
				malformedEntries = true;
				current = undefined;
				continue;
			}
			current = { id: Number(heading[1]), headingLine: item.line };
			entries.push(current);
			continue;
		}
		if (current === undefined || current.quote !== undefined) continue;
		const quote = /"([^"]+)"/.exec(item.text)?.[1];
		if (quote !== undefined) current.quote = quote;
	}
	for (const [index, entry] of entries.entries()) {
		if (entry.id !== index + 1) {
			out.push({
				file: path,
				line: entry.headingLine,
				message: "errata IDs must be numbered E1..En in order",
			});
			malformedEntries = true;
		}
		if (entry.quote === undefined) {
			out.push({
				file: path,
				line: entry.headingLine,
				message: "errata entry requires a quoted wrong text",
			});
			malformedEntries = true;
		} else if (!lines.slice(0, headingIndex).join("\n").includes(entry.quote)) {
			out.push({
				file: path,
				line: entry.headingLine,
				message: "errata quoted text no longer appears above the section",
			});
		}
	}
	if (headerLine === undefined) {
		out.push({
			file: path,
			line: Math.max(1, h1Index + 1),
			message: "Errata section requires an Errata marker line",
		});
	} else if (finalH2 && !malformedEntries) {
		const marker = headerLine.text.slice(headerLine.text.indexOf(":") + 1);
		const markerItems = marker.split(/\s(?:—|--|\(see\b)/i, 1)[0] ?? "";
		const markerIds = [...markerItems.matchAll(/(?:^|,)\s*\[?E(\d+)\b/g)].map(
			(match) => Number(match[1]),
		);
		if (
			markerIds.length !== entries.length ||
			markerIds.some((id, index) => id !== entries[index]?.id)
		)
			out.push({
				file: path,
				line: headerLine.line,
				message: "Errata marker IDs must match section entry IDs in order",
			});
	}
	return out;
}

export async function runOnce(
	deps: Deps,
	config: LedgerConfig,
): Promise<number> {
	const {
		root,
		readText,
		listDesignFiles,
		readRecord,
		readBaseDecisionPaths,
		changed,
		log,
		err,
	} = deps;
	let paths: string[];
	try {
		paths = await listDesignFiles(root, config.designsRoot);
	} catch (error) {
		err(`design-ledger-gate: cannot read the tree at ${root}`);
		err(errorMessage(error));
		return 2;
	}
	const decisions: string[] = [];
	const recordsPaths: string[] = [];
	const strays: StrayPath[] = [];
	for (const file of paths) {
		const kind = classifyDesignPath(file, config);
		if (kind === "decision") decisions.push(file);
		if (kind === "legacy-ledger" || kind === "misplaced")
			strays.push({ path: file, kind });
		if (touchesRecord(file, config)) recordsPaths.push(file);
	}
	const decisionSet = new Set(decisions);
	const recordSet = new Set(recordsPaths);
	const contents = new Map<string, string>();
	const decisionFiles: Array<{ path: string; text: string }> = [];
	const records: RecordHeader[] = [];
	const violations: Violation[] = [];
	try {
		for (const file of [...new Set([...decisions, ...recordsPaths])].sort()) {
			const text = await readText(root, file);
			if (text === null) continue;
			contents.set(file, text);
			if (decisionSet.has(file)) {
				decisionFiles.push({ path: file, text });
				violations.push(...conflictMarkerViolations(file, text));
			}
			if (recordSet.has(file)) {
				records.push(parseRecordHeader(file, text));
				violations.push(...conflictMarkerViolations(file, text));
			}
		}
	} catch (error) {
		err(`design-ledger-gate: cannot read the tree at ${root}`);
		err(errorMessage(error));
		return 2;
	}
	const corpus = buildDecisionCorpus(decisionFiles, strays);
	violations.push(
		...evaluate(
			corpus,
			records,
			changed,
			(path) => readRecord(root, path),
			config,
		),
	);
	let citationsSeen = 0;
	let citationsChecked = 0;
	let citationsUnresolvable = 0;
	let citationsRepoAmbiguous = 0;
	if (config.legs?.citations)
		for (const file of paths.filter(
			(path) =>
				path.startsWith(`${config.designsRoot}/`) && path.endsWith(".md"),
		)) {
			const text = contents.get(file) ?? (await readText(root, file));
			if (text === null) continue;
			const scan = markdownLinesOutsideFences(text);
			for (const { line, text: source } of scan.lines) {
				for (const match of source.matchAll(CITATION_RE)) {
					const targetPath = match[1];
					const references = match[2];
					if (targetPath === undefined || references === undefined) continue;
					for (const reference of references.split(",")) {
						const range = /^\s*(\d+)(?:-(\d+))?\s*$/.exec(reference);
						if (range === null) continue;
						const first = Number(range[1]);
						const last = Number(range[2] ?? range[1]);
						if (first === 0 || last === 0 || last < first) continue;
						citationsSeen++;
						const relativeTarget = pathPosix.normalize(
							pathPosix.join(pathPosix.dirname(file), targetPath),
						);
						if (!withinRepo(root, relativeTarget)) {
							violations.push({
								file,
								line,
								message: `citation target escapes the repository: ${targetPath}`,
							});
							continue;
						}
						let target = await readText(root, relativeTarget);
						if (target === null) {
							if (!targetPath.includes("/")) {
								citationsUnresolvable++;
								continue;
							}
							const rootTarget = pathPosix.normalize(targetPath);
							if (
								config.citationAmbiguousPaths?.includes(rootTarget) === true
							) {
								citationsRepoAmbiguous++;
								continue;
							}
							if (!withinRepo(root, rootTarget)) {
								citationsUnresolvable++;
								continue;
							}
							target = await readText(root, rootTarget);
						}
						if (target === null) {
							citationsUnresolvable++;
							continue;
						}
						citationsChecked++;
						const count =
							target.length === 0
								? 0
								: target.split("\n").length - (target.endsWith("\n") ? 1 : 0);
						if (first > count || last > count)
							violations.push({
								file,
								line,
								message: `citation ${targetPath}:${first}${last === first ? "" : `-${last}`} exceeds target line count ${count}`,
							});
					}
				}
			}
			if (scan.openFenceLine !== null)
				violations.push({
					file,
					line: scan.openFenceLine,
					message: "unclosed Markdown fence",
				});
		}
	if (config.legs?.recordLinks)
		for (const file of paths.filter(
			(path) =>
				path.startsWith(`${config.designsRoot}/`) && path.endsWith(".md"),
		)) {
			const text = contents.get(file) ?? (await readText(root, file));
			if (text === null) continue;
			const links = /\]\(([^)#]+\.md)#([^)]*)\)/g;
			for (const { line, text: source } of markdownLinesOutsideFences(text)
				.lines)
				for (const match of source.matchAll(links)) {
					const targetPath = match[1];
					const anchor = match[2];
					if (targetPath === undefined || anchor === undefined) continue;
					const relativeTarget = pathPosix.normalize(
						pathPosix.join(pathPosix.dirname(file), targetPath),
					);
					if (!withinRepo(root, relativeTarget)) {
						violations.push({
							file,
							line,
							message: `relative Markdown link escapes the repository (DL-221): ${targetPath}`,
						});
						continue;
					}
					const target = await readText(root, relativeTarget);
					if (target !== null && !headingSlugs(target).has(anchor))
						violations.push({
							file,
							line,
							message: `record link anchor not found: ${targetPath}#${anchor}`,
						});
				}
		}
	if (config.legs?.errata)
		for (const file of paths.filter(
			(path) =>
				path.startsWith(`${config.designsRoot}/`) && path.endsWith(".md"),
		)) {
			const text = contents.get(file) ?? (await readText(root, file));
			if (text !== null) violations.push(...validateErrata(file, text));
		}
	if (config.legs?.mainIds && changed.files.length > 0) {
		try {
			const basePaths = await readBaseDecisionPaths(
				root,
				process.env.BASE_REF ?? "main",
			);
			const baseIds = new Set(
				basePaths.map((path) => pathPosix.basename(path).replace(/\.md$/, "")),
			);
			for (const row of corpus.rows.filter(
				(decision) =>
					changed.files.includes(decision.path) &&
					!basePaths.includes(decision.path),
			))
				if (baseIds.has(row.id))
					violations.push({
						file: row.path,
						line: KEY_LINE.id,
						message: `${row.id} already exists on base branch tip`,
					});
		} catch (error) {
			err(
				`design-ledger-gate: cannot read base decision listing: ${errorMessage(error)}`,
			);
			return 2;
		}
	}
	if (violations.length === 0) {
		const pr =
			changed.body === null
				? "PR checks skipped (no PR context)"
				: "PR checks enabled";
		const citations = config.legs?.citations
			? `${citationsChecked}/${citationsSeen} citation(s) (${citationsUnresolvable} unresolvable, ${citationsRepoAmbiguous} repo-ambiguous)`
			: "citation checks off";
		log(
			`design-ledger-gate: OK — ${corpus.rows.length} decision file(s), ${records.length} record(s) status-checked; ${pr}; ${citations}.`,
		);
		return 0;
	}
	violations.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line);
	err("");
	err(`design-ledger-gate: ${violations.length} violation(s):`);
	for (const { file, line, message } of violations)
		err(line > 0 ? `  ${file}:${line}: ${message}` : `  ${file}: ${message}`);
	err("");
	if (config.remediationDoc) err(`See ${config.remediationDoc}.`);
	return 1;
}

export type PrContext =
	| { kind: "pr"; repo: string; prNumber: string }
	| { kind: "skip" }
	| { kind: "error"; message: string };
export function prContextFrom(
	env: Readonly<Record<string, string | undefined>>,
): PrContext {
	const repo = env.REPO ?? "";
	const prNumber = env.PR_NUMBER ?? "";
	if (repo !== "" && /^[1-9][0-9]*$/.test(prNumber))
		return { kind: "pr", repo, prNumber };
	if (
		env.GITHUB_EVENT_NAME === "pull_request" ||
		(env.CI_PIPELINE_EVENT === "pull_request" &&
			env.CI_PIPELINE_FILES === undefined) ||
		prNumber !== ""
	)
		return { kind: "error", message: "the PR checks need valid PR context" };
	return { kind: "skip" };
}

function parseWoodpeckerFiles(value: string | undefined): string[] | null {
	if (value === undefined) return null;
	try {
		const parsed: unknown = JSON.parse(value);
		if (
			!Array.isArray(parsed) ||
			!parsed.every((file) => typeof file === "string")
		)
			throw new Error("CI_PIPELINE_FILES must be a JSON array of strings");
		return parsed;
	} catch (error) {
		throw new Error(`invalid CI_PIPELINE_FILES: ${errorMessage(error)}`);
	}
}

function scanRoot(): string {
	if (process.env.GATE_ROOT !== undefined)
		return resolve(process.env.GATE_ROOT);
	const result = Bun.spawnSync({
		cmd: ["git", "rev-parse", "--show-toplevel"],
		stdout: "pipe",
		stderr: "pipe",
	});
	if (result.exitCode !== 0)
		throw new Error(
			`cannot find git root: ${new TextDecoder().decode(result.stderr).trim()}`,
		);
	return new TextDecoder().decode(result.stdout).trim();
}

function configArgument(args: readonly string[]): string | undefined {
	if (args.length === 1 && args[0] === "--help") return "help";
	return args.length === 2 && args[0] === "--config" ? args[1] : undefined;
}

async function main(args: readonly string[]): Promise<number> {
	const configPath = configArgument(args);
	if (configPath === "help") {
		console.log("Usage: design-ledger-gate --config <path>");
		return 0;
	}
	if (configPath === undefined) {
		console.error("Usage: design-ledger-gate --config <path>");
		return 2;
	}
	try {
		const config = loadLedgerConfig(configPath);
		const root = scanRoot();
		let changed: Changed = { files: [], body: null, headBranch: "" };
		const woodpeckerFiles =
			process.env.CI_PIPELINE_EVENT === "pull_request"
				? parseWoodpeckerFiles(process.env.CI_PIPELINE_FILES)
				: null;
		const context = prContextFrom(process.env);
		if (context.kind === "error" && woodpeckerFiles === null)
			throw new Error(context.message);
		if (woodpeckerFiles !== null)
			changed = {
				files: woodpeckerFiles,
				body: process.env.CI_COMMIT_PULL_REQUEST_BODY ?? "",
				headBranch: process.env.CI_COMMIT_SOURCE_BRANCH ?? "",
			};
		else if (context.kind === "pr") {
			const view =
				await $`timeout 30 gh pr view ${context.prNumber} --repo ${context.repo} --json headRefName,body`.json();
			const files =
				await $`timeout 60 gh api --paginate repos/${context.repo}/pulls/${context.prNumber}/files --jq .[].filename`.text();
			changed = {
				files: files.split("\n").filter((line) => line.length > 0),
				body: view.body,
				headBranch: view.headRefName,
			};
		}
		return await runOnce(
			{
				root,
				readText: async (workspaceRoot, file) => {
					const entry = Bun.file(resolve(workspaceRoot, file));
					return (await entry.exists()) ? entry.text() : null;
				},
				listDesignFiles: async (workspaceRoot, designsRoot) => {
					const out: string[] = [];
					for await (const file of new Bun.Glob(`${designsRoot}/**`).scan({
						cwd: workspaceRoot,
						onlyFiles: true,
					}))
						out.push(file.replaceAll("\\", "/"));
					return out.sort();
				},
				readRecord: (workspaceRoot, file) => {
					try {
						const absolute = resolve(workspaceRoot, file);
						return existsSync(absolute)
							? recordContentFromText(readFileSync(absolute, "utf8"))
							: null;
					} catch {
						return null;
					}
				},
				readBaseDecisionPaths: async (workspaceRoot, baseRef) => {
					const result = Bun.spawnSync({
						cmd: [
							"git",
							"-C",
							workspaceRoot,
							"ls-tree",
							"-r",
							"--name-only",
							`origin/${baseRef}`,
						],
						stdout: "pipe",
						stderr: "pipe",
					});
					if (result.exitCode !== 0)
						throw new Error(new TextDecoder().decode(result.stderr).trim());
					return new TextDecoder()
						.decode(result.stdout)
						.split("\n")
						.filter((file) => classifyDesignPath(file, config) === "decision");
				},
				changed,
				log: (message) => console.log(message),
				err: (message) => console.error(message),
			},
			config,
		);
	} catch (error) {
		console.error(`design-ledger-gate: ${errorMessage(error)}`);
		return 2;
	}
}

if (import.meta.main) process.exitCode = await main(process.argv.slice(2));
