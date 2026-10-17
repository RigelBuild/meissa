import { afterAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	allPackages,
	type Deps,
	isPublished,
	parsePaths,
	publishAll,
} from "./publish.ts";

const tree = mkdtempSync(join(tmpdir(), "publish-"));
afterAll(() => rmSync(tree, { recursive: true, force: true }));

test("a manual dispatch lists exactly the public packages, sorted", () => {
	const pkgs: Record<string, object> = {
		zeta: { name: "@rigelbuild/zeta" },
		alpha: { name: "@rigelbuild/alpha", private: false },
		internal: { name: "@rigelbuild/internal", private: true },
	};
	for (const [dir, manifest] of Object.entries(pkgs)) {
		mkdirSync(join(tree, "packages", dir), { recursive: true });
		writeFileSync(
			join(tree, "packages", dir, "package.json"),
			JSON.stringify(manifest),
		);
	}
	writeFileSync(join(tree, "packages", "README.md"), "not a package");
	expect(allPackages(tree)).toEqual(["packages/alpha", "packages/zeta"]);
});

const registry =
	(statuses: Record<string, number>): Deps["fetch"] =>
	async (url) => ({ status: statuses[url] ?? 404 });

const deps = (statuses: Record<string, number>) => {
	const published: string[] = [];
	const lines: string[] = [];
	const d: Deps = {
		read: (path) => ({
			name: `@rigelbuild/${path.split("/")[1]}`,
			version: "1.0.0",
		}),
		fetch: registry(statuses),
		publish: (path) => published.push(path),
		log: (line) => lines.push(line),
	};
	return { d, published, lines };
};

const url = (pkg: string) =>
	`https://registry.npmjs.org/@rigelbuild%2f${pkg}/1.0.0`;

describe("parsePaths", () => {
	test("accepts a non-empty string array", () => {
		expect(parsePaths('["packages/a","packages/b"]')).toEqual([
			"packages/a",
			"packages/b",
		]);
	});

	test.each([[""], ["[]"], ['"packages/a"'], ["[1]"], [undefined]])(
		"rejects %p",
		(raw) => {
			expect(() => parsePaths(raw)).toThrow();
		},
	);
});

describe("isPublished", () => {
	test("fails on a registry error instead of publishing", async () => {
		await expect(
			isPublished(
				{ name: "@rigelbuild/a", version: "1.0.0" },
				registry({ [url("a")]: 503 }),
			),
		).rejects.toThrow("registry returned 503");
	});
});

describe("publishAll", () => {
	test("re-run skips versions already on npm and publishes the rest", async () => {
		const { d, published, lines } = deps({ [url("gate")]: 200 });
		await publishAll(["packages/gate", "packages/list"], d);
		expect(published).toEqual(["packages/list"]);
		expect(lines).toEqual([
			"skip @rigelbuild/gate@1.0.0: already on npm",
			"published @rigelbuild/list@1.0.0",
		]);
	});

	test("stops at the first failed publish", async () => {
		const { d } = deps({});
		d.publish = (path) => {
			throw new Error(`npm publish failed in ${path}`);
		};
		await expect(publishAll(["packages/a", "packages/b"], d)).rejects.toThrow(
			"packages/a",
		);
	});
});
