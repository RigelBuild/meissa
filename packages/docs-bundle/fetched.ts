import { readFile } from "node:fs/promises";
import type { BundleSource, ConfigMode, FetchedSource } from "./types.ts";

function stringField(value: Record<string, unknown>, name: string): string {
	const field = value[name];
	if (typeof field !== "string")
		throw new Error(`fetched source ${name} must be a string`);
	return field;
}

function parseSource(value: unknown): FetchedSource {
	if (typeof value !== "object" || value === null || Array.isArray(value)) {
		throw new Error("fetched source must be an object");
	}
	const input = value as Record<string, unknown>;
	const config = stringField(input, "config");
	if (config !== "required" && config !== "default")
		throw new Error("fetched source config must be required or default");
	const source: BundleSource = {
		repo: stringField(input, "repo"),
		mount: stringField(input, "mount"),
		config: config satisfies ConfigMode,
	};
	return {
		...source,
		root: stringField(input, "root"),
		sha: stringField(input, "sha"),
	};
}

export async function readFetched(path: string): Promise<FetchedSource[]> {
	let content: string;
	try {
		content = await readFile(path, "utf8");
	} catch (error) {
		if (error instanceof Error && "code" in error && error.code === "ENOENT")
			return [];
		throw error;
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(content);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`invalid fetched source file: ${message}`, {
			cause: error,
		});
	}
	if (!Array.isArray(parsed))
		throw new Error("fetched source file must contain an array");
	return parsed.map(parseSource);
}
