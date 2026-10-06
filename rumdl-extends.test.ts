import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const directories: string[] = [];
const baseConfig = process.env.RUMDL_BASE_CONFIG;
const rumdl = process.env.RUMDL_BIN;
const pathRumdl = Bun.which("rumdl");

if (!baseConfig) throw new Error("RUMDL_BASE_CONFIG is not set");
if (!rumdl) throw new Error("RUMDL_BIN is not set");
if (pathRumdl !== rumdl) {
  throw new Error(`PATH rumdl (${pathRumdl ?? "not found"}) does not match RUMDL_BIN`);
}

async function checkWithConfig(config: string): Promise<{ status: number; output: string }> {
  const directory = await mkdtemp(join(tmpdir(), "meissa-rumdl-extends-"));
  directories.push(directory);
  const configPath = join(directory, ".rumdl.toml");
  const markdownPath = join(directory, "headingless.md");
  await writeFile(configPath, config);
  const paragraph = "A paragraph whose length exceeds the default maximum line width without violating other default rules. ".repeat(2).trimEnd();
  await writeFile(markdownPath, `${paragraph}\n`);

  const result = Bun.spawnSync(
    [rumdl, "check", "--no-cache", "--config", configPath, markdownPath],
    {
      cwd: directory,
      env: { ...process.env, RUMDL_BASE_CONFIG: baseConfig },
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  return {
    status: result.exitCode ?? 1,
    output: `${result.stdout.toString()}${result.stderr.toString()}`,
  };
}

afterEach(async () => {
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

describe("shared rumdl policy inheritance", () => {
  test("extends inherits disabled MD013 for a long heading-less paragraph", async () => {
    const result = await checkWithConfig('extends = "$RUMDL_BASE_CONFIG"\n\n[MD041]\nenabled = false\n');
    expect(result.status).toBe(0);
  });

  test("the heading-less control without extends reports MD013", async () => {
    const result = await checkWithConfig("[MD041]\nenabled = false\n");
    expect(result.status).toBe(1);
    expect(result.output).toContain("MD013");
  });
});
