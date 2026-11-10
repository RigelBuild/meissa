import { afterAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const roots: string[] = [];

async function makeRepo(): Promise<string> {
	const root = await mkdtemp(join(tmpdir(), "docs-bundle-cli-"));
	roots.push(root);
	await writeFile(
		join(root, "docs-bundle.toml"),
		`exclude = []\n\n[[sections]]\nid = "docs"\nlabel = "Docs"\nroute = "/docs"\nprefix = "docs/"\n`,
	);
	await mkdir(join(root, "docs"), { recursive: true });
	return root;
}

async function check(
	root: string,
): Promise<{ exitCode: number | null; stderr: string }> {
	const result = Bun.spawnSync({
		cmd: [
			"bun",
			join(import.meta.dir, "cli.ts"),
			"check",
			"--root",
			root,
			"--config",
			"required",
		],
		cwd: import.meta.dir,
		stdout: "pipe",
		stderr: "pipe",
	});
	return {
		exitCode: result.exitCode,
		stderr: new TextDecoder().decode(result.stderr),
	};
}

afterAll(async () => {
	await Promise.all(
		roots.map((root) => rm(root, { recursive: true, force: true })),
	);
});

describe("docs-bundle check", () => {
	test("exits 1 and reports a broken relative link", async () => {
		const root = await makeRepo();
		await writeFile(join(root, "docs", "index.md"), "[missing](missing.md)\n");
		const result = await check(root);
		expect(result.exitCode).toBe(1);
		expect(result.stderr).toContain("docs-bundle: 1 broken link(s)");
	});

	test("exits 1 and reports a route collision", async () => {
		const root = await makeRepo();
		await mkdir(join(root, "docs", "first"), { recursive: true });
		await mkdir(join(root, "docs", "second"), { recursive: true });
		await writeFile(join(root, "docs", "first", "readme.md"), "# First\n");
		await writeFile(join(root, "docs", "second", "readme.md"), "# Second\n");
		await writeFile(
			join(root, "docs-bundle.toml"),
			`exclude = []\n\n[[sections]]\nid = "one"\nlabel = "One"\nroute = "/same"\nprefix = "docs/first/"\n\n[[sections]]\nid = "two"\nlabel = "Two"\nroute = "/same"\nprefix = "docs/second/"\n`,
		);
		const result = await check(root);
		expect(result.exitCode).toBe(1);
		expect(result.stderr).toContain("docs-bundle: 2 page(s)");
	});

	test("exits 2 when the required config is missing", async () => {
		const root = await makeRepo();
		await rm(join(root, "docs-bundle.toml"));
		const result = await check(root);
		expect(result.exitCode).toBe(2);
		expect(result.stderr).toContain("docs-bundle: configuration error");
	});
});
