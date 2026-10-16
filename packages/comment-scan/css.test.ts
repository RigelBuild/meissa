import { describe, expect, test } from "bun:test";
import { scanText } from "./index.ts";

describe("CSS", () => {
	test("// is not a comment in CSS", () => {
		expect(scanText("css", "a { width: calc(1px // 2); }").residues[0]).toBe(
			"a { width: calc(1px // 2); }",
		);
	});

	test("quoted URL targets keep block comment markers as content", () => {
		expect(
			scanText("css", 'src: url("/fonts/a/*b.otf"); /* note */').residues[0],
		).toBe('src: url("/fonts/a/*b.otf");');
	});

	test("unquoted URL targets keep comment markers as content", () => {
		const result = scanText("css", "src: url(/fonts/a/*b.otf); color: red;");
		expect(result.ambiguous).toBe(false);
		expect(result.residues[0]).toBe("src: url(/fonts/a/*b.otf); color: red;");
	});

	test("uppercase URL function names are recognized", () => {
		expect(
			scanText("css", "src: URL(/a/*b.otf); color: red;").residues[0],
		).toBe("src: URL(/a/*b.otf); color: red;");
	});

	test("escaped parentheses do not close unquoted URL targets", () => {
		expect(
			scanText("css", "src: url(/a\\)b/*c.otf); color: red;").residues[0],
		).toBe("src: url(/a\\)b/*c.otf); color: red;");
	});

	test("comments following a quoted URL are stripped", () => {
		const result = scanText(
			"css",
			'src: url("/fonts/a(b).otf");\n/* note */\na { color: red; }',
		);
		expect(result.ambiguous).toBe(false);
		expect(result.residues[1]).toBe("");
		expect(result.residues[2]).toBe("a { color: red; }");
	});

	test("CSS suppression comments remain in code residue", () => {
		for (const directive of [
			"/* stylelint-disable-next-line rule */",
			"/* biome-ignore lint/style/noX: reason */",
		]) {
			expect(scanText("css", directive).residues[0]).toBe(directive);
		}
	});
});
