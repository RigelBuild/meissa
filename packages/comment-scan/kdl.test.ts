import { describe, expect, test } from "bun:test";
import { scanText } from "./index.ts";

describe("KDL residues", () => {
	test("line and block comment markers in strings remain content", () => {
		expect(scanText("kdl", 'url "http://a/b" // note').residues[0]).toBe(
			'url "http://a/b"',
		);
		expect(scanText("kdl", 'p #"a//b"# // note').residues[0]).toBe(
			'p #"a//b"#',
		);
	});

	test("slashdash remains code", () => {
		expect(scanText("kdl", '/-bind "x" { Quit; }').residues[0]).toBe(
			'/-bind "x" { Quit; }',
		);
	});

	test("block comments nest", () => {
		expect(
			scanText(
				"kdl",
				"/* outer /* inner */ still comment */ node 1",
			).residues[0]?.trim(),
		).toBe("node 1");
	});
});
