import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const directories: string[] = [];
const baseConfig = process.env.RUMDL_BASE_CONFIG ?? join(import.meta.dir, "rumdl/base.toml");
const rumdl = Bun.which("rumdl");

async function checkWithConfig(config: string): Promise<number> {
  if (!baseConfig) throw new Error("RUMDL_BASE_CONFIG is not set");
  if (!rumdl) throw new Error("rumdl is not on PATH");

  const directory = await mkdtemp(join(tmpdir(), "meissa-rumdl-extends-"));
  directories.push(directory);
  const configPath = join(directory, ".rumdl.toml");
  const markdownPath = join(directory, "headingless.md");
  await writeFile(configPath, config);
  const paragraph = "A paragraph whose length exceeds the default maximum line width without violating other default rules. ".repeat(2).trimEnd();
  await writeFile(markdownPath, `${paragraph}\n`);

  return Bun.spawnSync(
    [rumdl, "check", "--no-cache", "--config", configPath, markdownPath],
    {
      cwd: directory,
      env: { ...process.env, RUMDL_BASE_CONFIG: baseConfig },
      stdout: "pipe",
      stderr: "pipe",
    },
  ).exitCode ?? 1;
}

afterEach(async () => {
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

describe("shared rumdl policy inheritance", () => {
  test("extends passes a heading-less file under the shared line-length rule", async () => {
    const status = await checkWithConfig('extends = "$RUMDL_BASE_CONFIG"\n\n[MD041]\nenabled = false\n');
    expect(status).toBe(0);
  });

  test("the same heading-less control without extends fails the default line-length rule", async () => {
    const status = await checkWithConfig("[MD041]\nenabled = false\n");
    expect(status).toBe(1);
  });
});
