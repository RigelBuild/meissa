// biome-ignore-all lint/suspicious/noTemplateCurlyInString: Nix and shell fixtures intentionally test literal interpolation markers.
import { describe, expect, test } from "bun:test";
import { residueScannerFor, scanText } from "./index.ts";

// Keep lexical corner cases tied to their language config.
describe("scanText", () => {
	test("Rust nested block comments close at the right depth", () => {
		const result = scanText(
			"rust",
			"/* outer /* inner */ code()\nstill comment */ real()",
		);
		expect(result.residues[0]).toBe("");
		expect(result.residues[1]?.trim()).toBe("real()");
	});

	test("Go block comments do not nest", () => {
		expect(
			scanText("go", "/* outer /* inner */ code()").residues[0]?.trim(),
		).toBe("code()");
	});

	test("Rust lifetimes do not open character literals", () => {
		expect(
			scanText("rust", "fn f<'a>(x: &'a str) {} // note").residues[0],
		).toBe("fn f<'a>(x: &'a str) {}");
	});

	test("escaped quotes do not close strings early", () => {
		expect(scanText("ts", 'const s = "a\\"b"; // note').residues[0]).toBe(
			'const s = "a\\"b";',
		);
	});

	test("Python triple-quoted strings are code", () => {
		expect(scanText("python", '"""a docstring"""').residues[0]).toBe(
			'"""a docstring"""',
		);
	});
	test("unterminated single-line strings are ambiguous", () => {
		expect(scanText("toml", "value = 'unfinished\nnext = 1").ambiguous).toBe(
			true,
		);
	});

	test("unclosed block comments are ambiguous", () => {
		expect(scanText("ts", "/* unfinished").ambiguous).toBe(true);
	});

	test("YAML block scalars preserve their body and resume after dedent", () => {
		const result = scanText(
			"yaml",
			"script: |\n  # payload\n  text\n# note\nnext: 1",
		);
		expect(result.residues[1]).toBe("  # payload");
		expect(result.residues[3]).toBe("");
		expect(result.residues[4]).toBe("next: 1");
	});
	test("YAML block scalars preserve comment payload with CRLF input", () => {
		const result = scanText(
			"yaml",
			"script: |\r\n  # payload\r\n  text\r\n# note\r\nnext: 1",
		);
		expect(result.residues).toEqual([
			"script: |",
			"  # payload",
			"  text",
			"",
			"next: 1",
		]);
		expect(result.ambiguous).toBe(false);
	});

	test("YAML apostrophes in plain scalars do not open strings", () => {
		expect(
			scanText("yaml", "text: it's fine\nother: '# value'").residues[1],
		).toBe("other: '# value'");
	});

	test("TOML multiline strings keep hash-prefixed payload", () => {
		const result = scanText("toml", 'input = """\n# payload\n"""\n# note');
		expect(result.residues[1]).toBe("# payload");
		expect(result.residues[3]).toBe("");
	});

	test("SQL dollar-quoted bodies keep comments as payload", () => {
		const result = scanText("sql", "DO $$\n-- payload\nEND $$;\n-- note");
		expect(result.residues[1]).toBe("-- payload");
		expect(result.residues[3]).toBe("");
	});

	test("SQL tagged dollar quotes close only on their tag", () => {
		const result = scanText(
			"sql",
			"DO $body$\n-- payload\nSELECT $$ not a close $$;\n$body$;\n-- note",
		);
		expect(result.residues[1]).toBe("-- payload");
		expect(result.residues[4]).toBe("");
	});

	test("shell heredoc content stays code and scanning resumes", () => {
		const result = scanText(
			"shell",
			"cat <<EOF\n# payload\nEOF\n# note\necho done",
		);
		expect(result.residues[1]).toBe("# payload");
		expect(result.residues[3]).toBe("");
		expect(result.residues[4]).toBe("echo done");
	});

	test("shell parameter expansion hashes are code", () => {
		const parameter = "$" + "{x#prefix}";
		expect(scanText("shell", `echo ${parameter} # note`).residues[0]).toBe(
			`echo ${parameter}`,
		);
	});
	test("regex literals after return are not comments", () => {
		const result = scanText("ts", "return /[\\/]/;\nconst x = 1; // note");
		expect(result.ambiguous).toBe(false);
		expect(result.residues[0]).toContain("return /[\\/]/;");
	});

	test("an unterminated interpolation remains ambiguous", () => {
		expect(scanText("nix", '"${unfinished"').ambiguous).toBe(true);
	});
	test("TS template literals keep shell-style comments as code", () => {
		const result = scanText(
			"ts",
			["const script = `", "  # payload", "`; // note"].join("\n"),
		);
		expect(result.residues[1]).toBe("  # payload");
		expect(result.residues[2]).toBe("`;");
	});

	test("nested Nix interpolation strings keep hashes as code", () => {
		const result = scanText("nix", 'x = "a ${g "b#c"} d";');
		expect(result.ambiguous).toBe(false);
		expect(result.residues[0]).toBe('x = "a ${g "b#c"} d";');
	});

	test("Nix interpolation restores comment scanning after indented strings", () => {
		const source = [
			'"${pkgs.writeShellScript "task" \'\'',
			"  # payload",
			"''}\"",
			"# note",
			"value = 1;",
		].join("\n");
		const result = scanText("nix", source);
		expect(result.ambiguous).toBe(false);
		expect(result.residues[3]).toBe("");
		expect(result.residues[4]).toBe("value = 1;");
	});

	test("comments inside Nix interpolation are stripped", () => {
		expect(scanText("nix", 'x = "pre ${ /*n*/ v } post";').residues[0]).toBe(
			'x = "pre ${  v } post";',
		);
	});

	test("an escaped Nix interpolation opener stays inside the string", () => {
		const result = scanText(
			"nix",
			["value = ''", "  echo ''${VALUE}", "''; # note", "next = 2;"].join("\n"),
		);
		expect(result.residues[3]).toBe("next = 2;");
	});

	test("nested Nix braces do not close interpolation early", () => {
		const result = scanText(
			"nix",
			['"pre ${ { a = 1; }.a } post" # note', "next = 2;"].join("\n"),
		);
		expect(result.residues[0]).toBe('"pre ${ { a = 1; }.a } post"');
		expect(result.residues[1]).toBe("next = 2;");
	});

	test("KDL slashdash stays in the code residue", () => {
		expect(scanText("kdl", '/-bind "x" { Quit; }').residues[0]).toBe(
			'/-bind "x" { Quit; }',
		);
	});

	test("CSS URL targets treat comment markers as content", () => {
		expect(
			scanText("css", "src: url(/fonts/a/*b); color: red;").residues[0],
		).toBe("src: url(/fonts/a/*b); color: red;");
	});
	test("protobuf single-quoted strings containing comment markers stay code", () => {
		expect(
			scanText("proto", "option java_package = 'a//b'; // note").residues[0],
		).toBe("option java_package = 'a//b';");
	});

	test("protobuf strings containing comment markers stay code", () => {
		expect(
			scanText("proto", 'option java_package = "example//pkg"; // note')
				.residues[0],
		).toBe('option java_package = "example//pkg";');
	});
});

describe("residueScannerFor", () => {
	test("returns no scanner for unknown extensions", () => {
		expect(residueScannerFor(".unknown")).toBeNull();
	});

	test("routes TSX to the parser scanner", () => {
		const scan = residueScannerFor(".tsx");
		expect(scan?.("const E = () => <a>x//y</a>; // note").residues[0]).toBe(
			"const E = () => <a>x//y</a>;",
		);
	});

	test("routes proto to the lexical scanner", () => {
		expect(
			residueScannerFor(".proto")?.('// note\nsyntax = "proto3";').residues[0],
		).toBe("");
	});
});
