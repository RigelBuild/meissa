import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { Glob, TOML } from "bun";
import type { ConfigMode, PublishConfig, SectionRule } from "./types.ts";

function record(value: unknown, label: string): Record<string, unknown> {
	if (typeof value !== "object" || value === null || Array.isArray(value)) {
		throw new Error(`${label} must be a table`);
	}
	// SAFETY: TOML tables are non-array objects, as checked above, so keyed access is valid.
	return value as Record<string, unknown>;
}

function strings(value: unknown, label: string): string[] {
	if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) {
		throw new Error(`${label} must be an array of strings`);
	}
	return value;
}

function parseSection(value: unknown, index: number): SectionRule {
	const input = record(value, `sections[${index}]`);
	if (typeof input.id !== "string" || typeof input.label !== "string" || typeof input.route !== "string") {
		throw new Error(`sections[${index}] requires string id, label, and route`);
	}
	if (input.prefix !== undefined && typeof input.prefix !== "string") {
		throw new Error(`sections[${index}].prefix must be a string`);
	}
	const files = input.files === undefined ? undefined : strings(input.files, `sections[${index}].files`);
	if (input.pagefind !== undefined && typeof input.pagefind !== "boolean") {
		throw new Error(`sections[${index}].pagefind must be a boolean`);
	}
	if (input.collapsed !== undefined && typeof input.collapsed !== "boolean") {
		throw new Error(`sections[${index}].collapsed must be a boolean`);
	}
	if (input.prefix === undefined && files === undefined) {
		throw new Error(`sections[${index}] requires prefix or files`);
	}
	return {
		id: input.id,
		label: input.label,
		route: input.route,
		...(typeof input.prefix === "string" ? { prefix: input.prefix } : {}),
		...(files === undefined ? {} : { files }),
		...(typeof input.pagefind === "boolean" ? { pagefind: input.pagefind } : {}),
		...(typeof input.collapsed === "boolean" ? { collapsed: input.collapsed } : {}),
	};
}

function parsePackages(value: unknown): NonNullable<PublishConfig["packages"]> {
	const input = record(value, "packages");
	if (typeof input.route !== "string") throw new Error("packages.route must be a string");
	const allow = strings(input.allow, "packages.allow");
	if (typeof input.readme_depth !== "number" || !Number.isInteger(input.readme_depth) || input.readme_depth < 0) {
		throw new Error("packages.readme_depth must be a non-negative integer");
	}
	return { route: input.route, allow, readmeDepth: input.readme_depth };
}

export async function readPublishConfig(root: string, mode: ConfigMode): Promise<PublishConfig> {
	const path = join(root, "docs-bundle.toml");
	let source: string;
	try {
		source = await readFile(path, "utf8");
	} catch (error) {
		if (error instanceof Error && "code" in error && error.code === "ENOENT") {
			if (mode === "default") return { exclude: [], sections: [] };
			throw new Error(`required publish config is missing: ${path}`);
		}
		throw error;
	}

	let parsed: unknown;
	try {
		parsed = TOML.parse(source);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`invalid docs-bundle.toml: ${message}`, { cause: error });
	}
	const input = record(parsed, "docs-bundle.toml");
	if (!("exclude" in input)) throw new Error("docs-bundle.toml requires exclude");
	const exclude = strings(input.exclude, "exclude");
	const sectionsValue = input.sections ?? [];
	if (!Array.isArray(sectionsValue)) throw new Error("sections must be an array of tables");
	return {
		exclude,
		sections: sectionsValue.map(parseSection),
		...(input.packages === undefined ? {} : { packages: parsePackages(input.packages) }),
	};
}

export function matchesAny(path: string, patterns: readonly string[]): boolean {
	return patterns.some((pattern) => new Glob(pattern).match(path));
}
