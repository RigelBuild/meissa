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

	test('header paths survive " b/" inside a name and git C-quoting', () => {
		const spaced = parseDiff(
			[
				"diff --git a/x b/y.ts b/x b/y.ts",
				"new file mode 100644",
				"index 0000000..e69de29",
			].join("\n"),
		)[0];
		expect(spaced?.path).toBe("x b/y.ts");
		const tab = parseDiff(
			[
				'diff --git "a/t\\tb.ts" "b/t\\tb.ts"',
				"index 1111111..2222222 100644",
				'Binary files "a/t\\tb.ts" and "b/t\\tb.ts" differ',
			].join("\n"),
		)[0];
		expect(tab?.path).toBe("t\tb.ts");
		const quoted = parseDiff(
			[
				'diff --git "a/caf\\303\\251\\nx.ts" "b/caf\\303\\251\\nx.ts"',
				'--- "a/caf\\303\\251\\nx.ts"',
				'+++ "b/caf\\303\\251\\nx.ts"',
				"@@ -1 +1 @@",
				"-a",
				"+b",
			].join("\n"),
		)[0];
		expect([quoted?.oldPath, quoted?.path]).toEqual([
			"café\nx.ts",
			"café\nx.ts",
		]);
	});

	test("a CRLF-saved diff keeps clean header paths", () => {
		const [quoted, renamed] = parseDiff(
			[
				'diff --git "a/\\303\\251 x.ts" "b/\\303\\251 x.ts"',
				"index 1111111..2222222 100644",
				'--- "a/\\303\\251 x.ts"',
				'+++ "b/\\303\\251 x.ts"',
				"@@ -1 +1 @@",
				"-a",
				"+b",
				"diff --git a/old.ts b/new.ts",
				"similarity index 100%",
				"rename from old.ts",
				"rename to new.ts",
				"",
			].join("\r\n"),
		);
		expect([quoted?.oldPath, quoted?.path]).toEqual([
			"\u00e9 x.ts",
			"\u00e9 x.ts",
		]);
		expect(quoted?.hunks[0]?.lines.map((line) => line.text)).toEqual([
			"a\r",
			"b\r",
		]);
		expect([renamed?.oldPath, renamed?.path]).toEqual(["old.ts", "new.ts"]);
	});

	test("quoted astral characters and trailing spaces in names survive", () => {
		const emoji = parseDiff(
			[
				'diff --git "a/\u{1F600}\\tx.ts" "b/\u{1F600}\\tx.ts"',
				"new file mode 100644",
				"index 0000000..e69de29",
			].join("\n"),
		)[0];
		expect(emoji?.path).toBe("\u{1F600}\tx.ts");
		const trailing = parseDiff(
			[
				"diff --git a/src/x.ts  b/src/x.ts ",
				"index 1111111..2222222 100644",
				"--- a/src/x.ts \t",
				"+++ b/src/x.ts \t",
				"@@ -1 +1 @@",
				"-a",
				"+b",
			].join("\n"),
		)[0];
		expect([trailing?.oldPath, trailing?.path]).toEqual([
			"src/x.ts ",
			"src/x.ts ",
		]);
		const empty = parseDiff(
			[
				"diff --git a/src/x.ts  b/src/x.ts ",
				"new file mode 100644",
				"index 0000000..e69de29",
			].join("\n"),
		)[0];
		expect(empty?.path).toBe("src/x.ts ");
	});

	test("empty and binary files added or deleted are flagged by their mode header", () => {
		const added = parseDiff(
			[
				"diff --git a/pkg/.gitkeep b/pkg/.gitkeep",
				"new file mode 100644",
				"index 0000000..e69de29",
			].join("\n"),
		)[0];
		expect([added?.added, added?.deleted, added?.path]).toEqual([
			true,
			false,
			"pkg/.gitkeep",
		]);
		const deleted = parseDiff(
			[
				"diff --git a/img.png b/img.png",
				"deleted file mode 100644",
				"index 1111111..0000000",
				"Binary files a/img.png and /dev/null differ",
			].join("\n"),
		)[0];
		expect([deleted?.added, deleted?.deleted, deleted?.binary]).toEqual([
			false,
			true,
			true,
		]);
	});
});

test("extOf finds a case-insensitive filename extension", () => {
	expect(extOf("src/Component.TSX")).toBe(".tsx");
	expect(extOf("README")).toBe("");
});
