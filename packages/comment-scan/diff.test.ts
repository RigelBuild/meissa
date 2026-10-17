import { describe, expect, test } from "bun:test";
import { extOf, parseDiff } from "./index.ts";

function diff(path: string, hunk: string, header = "@@ -1,3 +1,3 @@"): string {
	return [
		`diff --git a/${path} b/${path}`,
		`--- a/${path}`,
		`+++ b/${path}`,
		header,
		hunk,
	].join("\n");
}

describe("parseDiff", () => {
	test("removed SQL comment-looking lines are hunk deletions", () => {
		const files = parseDiff(
			diff(
				"queries/g.sql",
				[" SELECT 1;", "--- a note", " SELECT 2;"].join("\n"),
			),
		);
		expect(files).toHaveLength(1);
		expect(files[0]?.path).toBe("queries/g.sql");
		expect(
			files[0]?.hunks[0]?.lines.map(({ kind, text }) => [kind, text]),
		).toEqual([
			[" ", "SELECT 1;"],
			["-", "-- a note"],
			[" ", "SELECT 2;"],
		]);
	});

	test("added lines beginning with plus signs remain additions", () => {
		const files = parseDiff(diff("x.md", [" a", "+++ b", " c"].join("\n")));
		expect(
			files[0]?.hunks[0]?.lines.map(({ kind, text }) => [kind, text]),
		).toEqual([
			[" ", "a"],
			["+", "++ b"],
			[" ", "c"],
		]);
	});

	test("file headers before the first hunk set the path", () => {
		const text = [
			"diff --git a/old.ts b/new.ts",
			"--- a/old.ts",
			"+++ b/new.ts",
			"@@ -1 +1 @@",
			"-const n = 1;",
			"+const n = 2;",
		].join("\n");
		expect(parseDiff(text)[0]?.path).toBe("new.ts");
	});

	test("deleted files retain their original path", () => {
		const text = [
			"diff --git a/gone.ts b/gone.ts",
			"--- a/gone.ts",
			"+++ /dev/null",
			"@@ -1 +0,0 @@",
			"-const n = 1;",
		].join("\n");
		const file = parseDiff(text)[0];
		expect(file?.deleted).toBe(true);
		expect(file?.path).toBe("gone.ts");
	});

	test("rename and mode records set metadataOnly", () => {
		const rename = [
			"diff --git a/old.ts b/new.ts",
			"similarity index 100%",
			"rename from old.ts",
			"rename to new.ts",
		].join("\n");
		const mode = [
			"diff --git a/script.sh b/script.sh",
			"old mode 100644",
			"new mode 100755",
		].join("\n");
		expect(parseDiff(rename)[0]?.metadataOnly).toBe(true);
		expect(parseDiff(mode)[0]?.metadataOnly).toBe(true);
	});

	test("a pure rename or copy keeps its source as oldPath", () => {
		const rename = parseDiff(
			[
				"diff --git a/src/old.ts b/src/new.ts",
				"similarity index 100%",
				"rename from src/old.ts",
				"rename to src/new.ts",
			].join("\n"),
		)[0];
		expect([rename?.oldPath, rename?.newPath]).toEqual([
			"src/old.ts",
			"src/new.ts",
		]);
		const copy = parseDiff(
			[
				"diff --git a/a.ts b/b.ts",
				"similarity index 100%",
				"copy from a.ts",
				"copy to b.ts",
			].join("\n"),
		)[0];
		expect([copy?.oldPath, copy?.newPath]).toEqual(["a.ts", "b.ts"]);
		const nested = parseDiff(
			[
				"diff --git a/a/old.ts b/b/new.ts",
				"similarity index 100%",
				"rename from a/old.ts",
				"rename to b/new.ts",
			].join("\n"),
		)[0];
		expect([nested?.oldPath, nested?.path]).toEqual(["a/old.ts", "b/new.ts"]);
	});

	test("binary diff records are identified", () => {
		const text = [
			"diff --git a/image.png b/image.png",
			"index 1111111..2222222 100644",
			"Binary files a/image.png and b/image.png differ",
		].join("\n");
		expect(parseDiff(text)[0]?.binary).toBe(true);
	});
});

test("extOf finds a case-insensitive filename extension", () => {
	expect(extOf("src/Component.TSX")).toBe(".tsx");
	expect(extOf("README")).toBe("");
});
