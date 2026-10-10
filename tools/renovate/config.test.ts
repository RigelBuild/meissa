import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

type Json5Record = Record<string, unknown>;

const root = join(import.meta.dir, "../..");
const botPath = join(import.meta.dir, "bot-config.json5");
const configPath = join(import.meta.dir, "config.json5");
const lockPath = join(root, "devenv.lock");

async function readJson5(path: string): Promise<Json5Record> {
  const parsed: unknown = Bun.JSON5.parse(await readFile(path, "utf8"));
  return asRecord(parsed, path);
}

function asRecord(value: unknown, label: string): Json5Record {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  return Object.fromEntries(Object.entries(value));
}

describe("Renovate policy", () => {
  test("bot scope, onboarding, and relock commands are pinned", async () => {
    const bot = await readJson5(botPath);
    expect(bot.repositories).toEqual(["RigelBuild/meissa"]);
    expect(bot.onboarding).toBe(false);
    expect(bot.allowedCommands).toEqual([
      "^devenv update nixpkgs$",
      "^nix flake update nixpkgs$",
    ]);
  });

  test("the workflow cron alone sets the cadence; nixpkgs and bun/npm managers have a 5-day cooldown", async () => {
    const config = await readJson5(configPath);
    // A Renovate schedule window misses runs once GitHub starts the cron hours late.
    expect(Array.isArray(config.extends)).toBe(true);
    expect(config.extends.filter((preset: unknown) => String(preset).startsWith("schedule:"))).toEqual([]);
    expect(config.schedule).toBeUndefined();
    expect(config.lockFileMaintenance).toBeUndefined();
    expect(Array.isArray(config.packageRules)).toBe(true);
    expect(config.packageRules.filter((entry: unknown) => "schedule" in asRecord(entry, "package rule"))).toEqual([]);
    expect(config.extends).not.toContain("helpers:pinGitHubActionDigests");
    expect(config.enabledManagers).toEqual(["custom.regex", "bun", "npm"]);
    expect(config.minimumReleaseAge).toBe("5 days");
    expect(config.internalChecksFilter).toBe("strict");
  });

  test("the cooldown exemptions match bunfig minimumReleaseAgeExcludes", async () => {
    const config = await readJson5(configPath);
    const bunfig: unknown = Bun.TOML.parse(await readFile(join(root, "bunfig.toml"), "utf8"));
    const install = asRecord(asRecord(bunfig, "bunfig.toml").install, "bunfig install");
    const rules = config.packageRules;
    expect(Array.isArray(rules)).toBe(true);
    const exempt = rules.map((entry) => asRecord(entry, "package rule")).filter((entry) =>
      entry.minimumReleaseAge === null && Array.isArray(entry.matchPackageNames)
    );
    expect(exempt.flatMap((entry) => entry.matchPackageNames)).toEqual(install.minimumReleaseAgeExcludes);
  });

  test("typescript stays on 6.x until 7.1 ships its API", async () => {
    const config = await readJson5(configPath);
    const rules = config.packageRules;
    expect(Array.isArray(rules)).toBe(true);
    const cap = rules.map((entry) => asRecord(entry, "package rule")).find((entry) =>
      Array.isArray(entry.matchPackageNames) && entry.matchPackageNames.includes("typescript")
    );
    const range = String(cap?.allowedVersions);
    expect(Bun.semver.satisfies("6.9.9", range)).toBe(true);
    expect(Bun.semver.satisfies("7.0.2", range)).toBe(false);
  });

  test("the nixpkgs lock manager has a same-branch relock with no cooldown", async () => {
    const config = await readJson5(configPath);
    const managers = config.customManagers;
    expect(Array.isArray(managers)).toBe(true);
    const manager = managers.map((entry) => asRecord(entry, "manager")).find((entry) =>
      entry.depNameTemplate === "cachix/devenv-nixpkgs"
    );
    expect(manager).toBeDefined();
    expect(manager?.managerFilePatterns).toEqual(["/^devenv\\.lock$/"]);
    expect(manager?.matchStrings).toEqual([
      "\"repo\": \"devenv-nixpkgs\",\\s*\"rev\": \"(?<currentDigest>[a-f0-9]{40})\"",
    ]);

    const rules = config.packageRules;
    expect(Array.isArray(rules)).toBe(true);
    const rule = rules.map((entry) => asRecord(entry, "package rule")).find((entry) =>
      Array.isArray(entry.matchDepNames) && entry.matchDepNames.includes("cachix/devenv-nixpkgs")
    );
    expect(rule?.schedule).toBeUndefined();
    expect(rule?.minimumReleaseAge).toBeNull();
    expect(asRecord(rule?.postUpgradeTasks, "postUpgradeTasks")).toMatchObject({
      commands: ["devenv update nixpkgs", "nix flake update nixpkgs"],
      fileFilters: ["devenv.lock", "flake.lock"],
      executionMode: "branch",
    });
  });

  test("the scheduled workflow mints its token in the main environment and prepares devenv", async () => {
    const workflow = await readFile(join(root, ".github/workflows/renovate.yml"), "utf8");
    expect(workflow).toContain('cron: "0 6 * * *"');
    expect(workflow).toContain("workflow_dispatch:");
    expect(workflow).toContain("environment: main");
    expect(workflow).toContain("actions/create-github-app-token@");
    expect(workflow).toContain("client-id: ${{ vars.RENOVATE_APP_CLIENT_ID }}");
    expect(workflow).toContain("private-key: ${{ secrets.RENOVATE_APP_PRIVATE_KEY }}");
    expect(workflow).toContain("RENOVATE_TOKEN: ${{ steps.app-token.outputs.token }}");
    expect(workflow).toContain("bunx renovate@44.46.2");

    const parsed: unknown = Bun.YAML.parse(workflow);
    const jobs = asRecord(asRecord(parsed, "workflow").jobs, "workflow jobs");
    const renovate = asRecord(jobs.renovate, "renovate job");
    const steps = renovate.steps;
    expect(Array.isArray(steps)).toBe(true);
    const stepRecords = steps.map((step) => asRecord(step, "workflow step"));
    const devenvIndex = stepRecords.findIndex((step) => step.name === "Put pinned devenv CLI on PATH");
    const devenvAssertIndex = stepRecords.findIndex((step) => step.name === "Assert devenv is on PATH");
    const nodeIndex = stepRecords.findIndex((step) => step.name === "Put pinned Node.js on PATH");
    const nodeAssertIndex = stepRecords.findIndex((step) => step.name === "Assert pinned Node.js is on PATH");
    const runIndex = stepRecords.findIndex((step) => step.name === "Run Renovate");
    expect(devenvIndex).toBeGreaterThanOrEqual(0);
    expect(devenvIndex).toBeLessThan(devenvAssertIndex);
    expect(devenvAssertIndex).toBeLessThan(runIndex);
    expect(stepRecords[devenvIndex]?.run).toContain("--inputs-from . nixpkgs#devenv");
    expect(stepRecords[devenvIndex]?.run).toContain("$GITHUB_PATH");
    expect(stepRecords[devenvAssertIndex]?.run).toBe("command -v devenv");
    expect(nodeIndex).toBeGreaterThanOrEqual(0);
    expect(nodeIndex).toBeLessThan(nodeAssertIndex);
    expect(nodeAssertIndex).toBeLessThan(runIndex);
    expect(stepRecords[nodeIndex]?.run).toContain("--inputs-from . nixpkgs#nodejs");
    expect(stepRecords[nodeIndex]?.run).toContain("$GITHUB_PATH");
    expect(stepRecords[nodeAssertIndex]?.run).toBe("node --version");
  });

  test("the seed lock tracks the older rolling nixpkgs node without follows", async () => {
    const lock: unknown = JSON.parse(await readFile(lockPath, "utf8"));
    const lockRecord = asRecord(lock, "devenv.lock");
    const nodes = asRecord(lockRecord.nodes, "devenv.lock nodes");
    const nixpkgs = asRecord(nodes.nixpkgs, "nixpkgs lock node");
    const locked = asRecord(nixpkgs.locked, "nixpkgs locked");
    expect(nixpkgs).not.toHaveProperty("follows");

    const yaml: unknown = Bun.YAML.parse(await readFile(join(root, "devenv.yaml"), "utf8"));
    const inputs = asRecord(asRecord(yaml, "devenv.yaml").inputs, "devenv.yaml inputs");
    const nixpkgsInput = asRecord(inputs.nixpkgs, "devenv.yaml nixpkgs input");
    expect(nixpkgsInput.url).toBe("github:cachix/devenv-nixpkgs/rolling");
    expect(nixpkgsInput).not.toHaveProperty("follows");

    const config = await readJson5(configPath);
    const managers = config.customManagers;
    expect(Array.isArray(managers)).toBe(true);
    const manager = managers.map((entry) => asRecord(entry, "manager")).find((entry) =>
      entry.depNameTemplate === "cachix/devenv-nixpkgs"
    );
    expect(manager).toBeDefined();
    const matchStrings = manager?.matchStrings;
    expect(Array.isArray(matchStrings)).toBe(true);
    const pattern = new RegExp(String(matchStrings?.[0]));
    expect(pattern.exec(await readFile(lockPath, "utf8"))?.groups?.currentDigest).toBe(locked.rev);
  });
});
