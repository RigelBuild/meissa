#!/usr/bin/env bun
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/** Every public package directory, for a manual dispatch that has no release list. */
export function allPackages(root: string): string[] {
	return readdirSync(join(root, "packages"), { withFileTypes: true })
		.filter((entry) => entry.isDirectory())
		.map((entry) => `packages/${entry.name}`)
		.filter(
			(path) =>
				!JSON.parse(readFileSync(join(root, path, "package.json"), "utf8"))
					.private,
		)
		.sort();
}

export type Manifest = { name: string; version: string };
export type Deps = {
	read: (path: string) => Manifest;
	fetch: (url: string) => Promise<{ status: number }>;
	publish: (path: string) => void;
	log: (line: string) => void;
};

/** Parses release-please's `paths_released`; an empty or malformed list must fail. */
export function parsePaths(raw: string | undefined): string[] {
	const parsed: unknown = JSON.parse(raw ?? "");
	if (
		!Array.isArray(parsed) ||
		parsed.length === 0 ||
		!parsed.every((p) => typeof p === "string")
	) {
		throw new Error("bad paths_released");
	}
	return parsed;
}

export async function isPublished(
	{ name, version }: Manifest,
	fetch: Deps["fetch"],
): Promise<boolean> {
	const url = `https://registry.npmjs.org/${name.replace("/", "%2f")}/${version}`;
	const { status } = await fetch(url);
	if (status === 200) return true;
	if (status === 404) return false;
	throw new Error(`${url}: registry returned ${status}`);
}

// Skipping already-published versions lets a re-run finish a release that stopped partway.
export async function publishAll(paths: string[], deps: Deps): Promise<void> {
	for (const path of paths) {
		const manifest = deps.read(path);
		const id = `${manifest.name}@${manifest.version}`;
		if (await isPublished(manifest, deps.fetch)) {
			deps.log(`skip ${id}: already on npm`);
			continue;
		}
		deps.publish(path);
		deps.log(`published ${id}`);
	}
}

if (import.meta.main) {
	const root = process.env.GITHUB_WORKSPACE ?? process.cwd();
	const paths =
		process.env.PUBLISH_ALL === "true"
			? allPackages(root)
			: parsePaths(process.env.PATHS_RELEASED);
	await publishAll(paths, {
		read: (path) =>
			JSON.parse(readFileSync(join(root, path, "package.json"), "utf8")),
		fetch: (url) => fetch(url),
		publish: (path) => {
			const result = Bun.spawnSync(
				["npm", "publish", "--provenance", "--access", "public"],
				{ cwd: join(root, path), stdout: "inherit", stderr: "inherit" },
			);
			if (result.exitCode !== 0)
				throw new Error(`npm publish failed in ${path}`);
		},
		log: (line) => console.log(line),
	});
}
