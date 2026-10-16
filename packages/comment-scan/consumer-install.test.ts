import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { fileURLToPath } from "node:url";

const temporaryDirectories: string[] = [];

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
});

test("packed package installs without a workspace and finds TSX comment blocks", async () => {
	const packageDirectory = fileURLToPath(new URL(".", import.meta.url));
	const packDirectory = mkdtempSync(join(tmpdir(), "comment-scan-pack-"));
	const isolatedDirectory = mkdtempSync(
		join(tmpdir(), "comment-scan-isolated-"),
	);
	temporaryDirectories.push(packDirectory, isolatedDirectory);
	const pack = Bun.spawnSync({
		cmd: ["bun", "pm", "pack", "--destination", packDirectory, "--quiet"],
		cwd: packageDirectory,
		stdout: "pipe",
		stderr: "pipe",
	});
	if (pack.exitCode !== 0) throw new Error(pack.stderr.toString());
	const archivePath = join(
		packDirectory,
		basename(pack.stdout.toString().trim()),
	);
	const isolatedArchive = join(isolatedDirectory, basename(archivePath));
	await Bun.write(isolatedArchive, Bun.file(archivePath));
	const install = Bun.spawnSync({
		cmd: ["bun", "add", isolatedArchive],
		cwd: isolatedDirectory,
		stdout: "pipe",
		stderr: "pipe",
	});
	if (install.exitCode !== 0) throw new Error(install.stderr.toString());
	const entry = join(
		isolatedDirectory,
		"node_modules",
		"@rigelbuild",
		"comment-scan",
		"index.ts",
	);
	const installed = await import(entry);
	const result = installed.commentBlocks(
		"// note\nconst View = () => <div />;",
		".tsx",
	);
	expect(result).toEqual({
		blocks: [{ start: 1, lines: 1, first: "// note", doc: false }],
		ambiguous: false,
	});
});
