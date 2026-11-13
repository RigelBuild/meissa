import { readFileSync } from "node:fs";
import { residueScannerFor } from "@rigelbuild/comment-scan";
import type { DocClass, LengthGateConfig } from "./core.ts";

function asRecord(value: unknown, label: string): Record<string, unknown> {
	if (typeof value !== "object" || value === null || Array.isArray(value))
		throw new Error(`${label} must be an object`);
	// SAFETY: JSON.parse returned a non-null, non-array object.
	return value as Record<string, unknown>;
}

function rejectUnknownKeys(
	record: Record<string, unknown>,
	allowed: readonly string[],
	label: string,
): void {
	for (const key of Object.keys(record)) {
		if (!allowed.includes(key))
			throw new Error(`Unknown key "${key}" in ${label}`);
	}
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

function readDocClass(value: unknown, index: number): DocClass {
	const label = `docs[${index}]`;
	const record = asRecord(value, label);
	rejectUnknownKeys(
		record,
		["name", "include", "exclude", "target", "markerAllowed"],
		label,
	);
	if (typeof record.name !== "string")
		throw new Error(`${label}.name must be a string`);
	if (typeof record.target !== "number" || !Number.isFinite(record.target))
		throw new Error(`${label}.target must be a finite number`);
	if (typeof record.markerAllowed !== "boolean")
		throw new Error(`${label}.markerAllowed must be a boolean`);
	return {
		name: record.name,
		include: readStringArray(record.include, `${label}.include`),
		exclude: readStringArray(record.exclude, `${label}.exclude`),
		target: record.target,
		markerAllowed: record.markerAllowed,
	};
}

export function loadConfig(path: string): LengthGateConfig {
	let parsed: unknown;
	try {
		parsed = JSON.parse(readFileSync(path, "utf8"));
	} catch (error) {
		throw new Error(
			`Cannot load config "${path}": ${error instanceof Error ? error.message : String(error)}`,
			{ cause: error },
		);
	}
	const config = asRecord(parsed, "Config");
	rejectUnknownKeys(config, ["extensions", "exclude", "docs"], "config");
	const extensions = readStringArray(config.extensions, "extensions");
	for (const extension of extensions) {
		if (residueScannerFor(extension) === null)
			throw new Error(
				`extensions includes ${JSON.stringify(extension)} but no scanner supports it`,
			);
	}
	if (!Array.isArray(config.docs))
		throw new Error("docs must be an array of document classes");
	return {
		extensions,
		exclude: readStringArray(config.exclude, "exclude"),
		docs: config.docs.map(readDocClass),
	};
}
