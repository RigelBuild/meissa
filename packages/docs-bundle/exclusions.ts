import { Glob } from "bun";

export function parseExclusions(source: string): string[] {
	let parsed: unknown;
	try {
		parsed = JSON.parse(source.replace(/^\s*\/\/.*$/gm, ""));
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`invalid exclusion config: ${message}`, { cause: error });
	}
	if (typeof parsed !== "object" || parsed === null || !("ignores" in parsed)) {
		throw new Error("exclusion config requires ignores");
	}
	const ignores = parsed.ignores;
	if (
		!Array.isArray(ignores) ||
		!ignores.every((value) => typeof value === "string")
	) {
		throw new Error("exclusion config ignores must be an array of strings");
	}
	return [...ignores, "**/outputs/**", "**/CHANGELOG.md"];
}

export function isExcluded(
	sourcePath: string,
	exclusions: readonly string[],
): boolean {
	return exclusions.some((pattern) => new Glob(pattern).match(sourcePath));
}
