import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { BundleManifest } from "./types.ts";

export async function writeManifest(distDir: string, manifest: BundleManifest): Promise<void> {
	await mkdir(distDir, { recursive: true });
	await writeFile(join(distDir, "_bundle.json"), `${JSON.stringify(manifest, null, 2)}\n`);
}
