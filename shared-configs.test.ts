import { afterAll, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const temporaryDirectories: string[] = [];

afterAll(() => {
	for (const directory of temporaryDirectories) {
		rmSync(directory, { recursive: true, force: true });
	}
});

describe("shared configuration packages", () => {
	test("Node resolves both exported config subpaths", () => {
		const result = Bun.spawnSync({
			cmd: [
				"node",
				"--input-type=module",
				"-e",
				'console.log(import.meta.resolve("@rigelbuild/tsconfig/base.json")); console.log(import.meta.resolve("@rigelbuild/biome-config/biome"))',
			],
			cwd: import.meta.dir,
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(0);
		const urls = new TextDecoder()
			.decode(result.stdout)
			.trim()
			.split("\n")
			.map(fileURLToPath);
		expect(urls).toEqual([
			join(import.meta.dir, "packages", "tsconfig", "base.json"),
			join(import.meta.dir, "packages", "biome-config", "biome.json"),
		]);
	});

	test("Node rejects the Biome subpath when its export key is removed", () => {
		const packageDirectory = join(
			import.meta.dir,
			"node_modules",
			"@rigelbuild",
			"biome-config",
		);
		const packageJson = join(packageDirectory, "package.json");
		const originalManifest = readFileSync(packageJson, "utf8");
		const manifestWithoutBiomeExport = originalManifest.replace(
			'"./biome": "./biome.json"',
			'"./other": "./biome.json"',
		);
		expect(manifestWithoutBiomeExport).not.toBe(originalManifest);
		try {
			writeFileSync(packageJson, manifestWithoutBiomeExport);
			const result = Bun.spawnSync({
				cmd: [
					"node",
					"--input-type=module",
					"-e",
					'import.meta.resolve("@rigelbuild/biome-config/biome")',
				],
				cwd: import.meta.dir,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(result.exitCode).not.toBe(0);
			expect(new TextDecoder().decode(result.stderr)).toContain(
				"ERR_PACKAGE_PATH_NOT_EXPORTED",
			);
		} finally {
			writeFileSync(packageJson, originalManifest);
		}
	});

	test("Biome CI accepts a consumer fixture extending the shared preset", () => {
		const directory = mkdtempSync(join(import.meta.dir, ".biome-preset-"));
		temporaryDirectories.push(directory);
		const configPath = join(directory, "biome.json");
		const fixturePath = join(directory, "fixture.ts");
		const config =
			'{\n\t"root": false,\n\t"extends": ["@rigelbuild/biome-config/biome"]\n}\n';
		writeFileSync(configPath, config);
		writeFileSync(
			fixturePath,
			"export const preset = {\n\tshared: true,\n};\n",
		);

		const result = Bun.spawnSync({
			cmd: ["biome", "ci", "--config-path", configPath, "."],
			cwd: directory,
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).toBe(0);
	});
});
