import { describe, expect, test } from "bun:test";
import { commentBlocks, isCgo } from "./index.ts";

function blocks(text: string, ext = ".go") {
	const result = commentBlocks(text, ext);
	if (result === null) throw new Error(`missing scanner for ${ext}`);
	return result;
}

describe("commentBlocks", () => {
	test("a five-line comment run is one block and a blank line splits runs", () => {
		const result = blocks(
			"// one\n// two\n// three\n// four\n// five\n\n// next\npackage p",
		);
		expect(result.blocks.map(({ start, lines }) => [start, lines])).toEqual([
			[1, 5],
			[7, 1],
		]);
	});

	test("a go:embed directive ends the comment block", () => {
		const result = blocks(
			"// one\n// two\n//go:embed data.txt\n// three\npackage p",
		);
		expect(result.blocks.map(({ start, lines }) => [start, lines])).toEqual([
			[1, 2],
			[4, 1],
		]);
	});

	test("slash doc comments and block doc comments are doc blocks", () => {
		const result = commentBlocks(
			"/// doc\nfunc f() {}\n\n/** block */\nfunc g() {}",
			".ts",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([true, true]);
	});

	test("Go package comments are doc comments regardless of package case", () => {
		for (const name of ["p", "Private", "MixedCase"]) {
			expect(blocks(`// package note\npackage ${name}`).blocks[0]?.doc).toBe(
				true,
			);
		}
	});

	test("Go exported and unexported standalone declarations classify", () => {
		const declarations = [
			"func Exported() {}",
			"func (r *T) Exported() {}",
			"const Exported = 1",
			"type Exported struct{}",
			"var Exported int",
		];
		const result = blocks(
			declarations.map((declaration) => `// note\n${declaration}`).join("\n\n"),
		);
		expect(result.blocks.map(({ doc }) => doc)).toEqual([
			true,
			true,
			true,
			true,
			true,
		]);
		const unexported = [
			"func hidden() {}",
			"func (r *T) hidden() {}",
			"const hidden = 1",
			"type hidden struct{}",
			"var hidden int",
		];
		expect(
			blocks(
				unexported.map((declaration) => `// note\n${declaration}`).join("\n\n"),
			).blocks.map(({ doc }) => doc),
		).toEqual([false, false, false, false, false]);
	});

	test("a blank line detaches Go declaration comments", () => {
		expect(blocks("// note\n\nfunc Exported() {}").blocks[0]?.doc).toBe(false);
	});

	test("Go grouped declarations are exported when any spec name is exported", () => {
		const text =
			"// group\nconst (\n\tprivate = 1\n\tExported = 2\n)\n\n// spec\nvar (\n\tValue, hidden int\n)";
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([true, true]);
		expect(
			blocks("// private group\nvar (\n\ta int\n\tb int\n)").blocks[0]?.doc,
		).toBe(false);
	});
	test("Go group comments classify exported names at the keyword and spec", () => {
		const text = [
			"// exported const group",
			"const (",
			"  hidden = 1",
			"  Exported = 2",
			")",
			"// const spec",
			"const (",
			"  // exported spec doc",
			"  ExportedSpec = 3",
			")",
			"// private const group",
			"const (",
			"  hidden = 4",
			")",
			"// private const spec",
			"const (",
			"  // private spec doc",
			"  hiddenSpec = 5",
			")",
			"// exported var group",
			"var (",
			"  hiddenVar int",
			"  ExportedVar int",
			")",
			"// var spec",
			"var (",
			"  // exported spec doc",
			"  ExportedVarSpec int",
			")",
			"// private var group",
			"var (",
			"  hiddenVar int",
			")",
			"// private var spec",
			"var (",
			"  // private spec doc",
			"  hiddenVarSpec int",
			")",
			"// exported type group",
			"type (",
			"  hiddenType struct{}",
			"  ExportedType struct{}",
			")",
			"// type spec",
			"type (",
			"  // exported spec doc",
			"  ExportedTypeSpec struct{}",
			")",
			"// private type group",
			"type (",
			"  hiddenType struct{}",
			")",
			"// private type spec",
			"type (",
			"  // private spec doc",
			"  hiddenTypeSpec struct{}",
			")",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([
			true,
			true,
			true,
			false,
			false,
			false,
			true,
			true,
			true,
			false,
			false,
			false,
			true,
			true,
			true,
			false,
			false,
			false,
		]);
	});

	test("Go group detection ignores parentheses inside string literals", () => {
		const text = '// group\nconst (\n  hidden = ")"\n  // )\n  Exported = 1\n)';
		expect(blocks(text).blocks[0]?.doc).toBe(true);
	});

	test("Go multiple names are exported if any name is exported", () => {
		const text =
			"// var exported\nvar a, B int\n\n// var private\nvar a, b int\n\n// const exported\nconst a, B = 1, 2\n\n// const private\nconst a, b = 1, 2";
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([
			true,
			false,
			true,
			false,
		]);
	});
	test("nested struct fields do not make a Go type group exported", () => {
		const result = blocks(
			"// group\ntype (\n  hidden struct {\n    Exported int\n  }\n)",
		);
		expect(result.blocks.map(({ doc }) => doc)).toEqual([false]);
	});
	test("Go grouped funcs and nested fields do not end group scanning", () => {
		const text = [
			"// hidden group",
			"type (",
			"  hidden func(",
			"    x int,",
			"  ) (",
			"    result int",
			"  )",
			"  Exported struct{}",
			")",
			"",
			"// field",
			"type (",
			"  hidden struct {",
			"    Exported int",
			"  }",
			")",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([true, false]);
	});
	test("an exported spec that opens parentheses still exports its group", () => {
		const text = "// group\nvar (\n  Exported func(\n    x int,\n  )\n)";
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([true]);
	});

	test("Go exported names use Unicode uppercase code points", () => {
		const text = [
			"// supplementary",
			"var 𐐀value int",
			"",
			"// titlecase",
			"var ǅvalue int",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([true, false]);
	});
	test("Go grouped multi-name const and var specs classify any exported name", () => {
		const text = [
			"// const exported",
			"const (",
			"  a, B = 1, 2",
			")",
			"",
			"// const private",
			"const (",
			"  a, b = 1, 2",
			")",
			"",
			"// var exported",
			"var (",
			"  a, B int",
			")",
			"",
			"// var private",
			"var (",
			"  a, b int",
			")",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([
			true,
			false,
			true,
			false,
		]);
	});
	test("proto scopes open when declaration braces start on the next line", () => {
		const text = [
			"message M",
			"{",
			"  // field",
			"  string value = 1;",
			"}",
			"service S",
			"{",
			"  // rpc",
			"  rpc Call (Req) returns (Res);",
			"}",
			"enum E",
			"{",
			"  // value",
			"  ZERO = 0;",
			"}",
		].join("\n");
		expect(blocks(text, ".proto").blocks.map(({ doc }) => doc)).toEqual([
			true,
			true,
			true,
		]);
	});

	test("cgo recognizes standalone and grouped imports with semicolons", () => {
		for (const text of [
			'package p\nimport "C";',
			'package p\nimport (\n"C"\n)',
			'package p\nimport (\n"fmt"\n"C"\n)',
			'package p\nimport (\n"C";\n)',
		]) {
			expect(isCgo(text)).toBe(true);
		}
	});

	test("raw strings containing import C do not classify as cgo", () => {
		expect(isCgo('package p\nvar source = `\nimport "C"\n`')).toBe(false);
	});

	test("Go grouped multi-name spec comments classify exported names", () => {
		const text = [
			"const (",
			"  // exported const spec",
			"  a, B = 1, 2",
			"  // private const spec",
			"  a, b = 3, 4",
			")",
			"var (",
			"  // exported var spec",
			"  a, B int",
			"  // private var spec",
			"  a, b int",
			")",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([
			true,
			false,
			true,
			false,
		]);
	});

	test("Go semicolon insertion keeps continued declaration names together", () => {
		for (const text of [
			"// doc\nvar a,\nB int",
			"// doc\nvar (\n a,\n B int\n)",
		]) {
			expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([true]);
		}
	});

	test("local declarations do not classify as package documentation", () => {
		const text = [
			"func run() {",
			"  // local var",
			"  var Exported int",
			"  // local const group",
			"  const (",
			"    Exported = 1",
			"  )",
			"}",
		].join("\n");
		expect(blocks(text).blocks.map(({ doc }) => doc)).toEqual([false, false]);
	});

	test("Go directives in trailing comments do not create cgo imports", () => {
		expect(isCgo('package p\nvar x = 1 //go:generate echo import "C"')).toBe(
			false,
		);
	});

	test("cgo tokenization supports same-line and grouped separators", () => {
		for (const text of [
			'package p; import "C"',
			'import ("C";)',
			'import ("C")',
			'package p\n//go:generate tool `\nimport "C"',
		]) {
			expect(isCgo(text)).toBe(true);
		}
	});

	test("grouped inline Go specs classify the first exported name", () => {
		expect(
			blocks("// g\nvar (Exported int;)").blocks.map(({ doc }) => doc),
		).toEqual([true]);
	});
	test("proto enum value comments attach to enum values", () => {
		expect(
			blocks("enum E {\n  // doc\n  ZERO = 0;\n}", ".proto").blocks.map(
				({ doc }) => doc,
			),
		).toEqual([true]);
	});

	test("proto comments attach to declarations without an intervening blank line", () => {
		const text = [
			"// Header",
			"",
			'syntax = "proto3";',
			"// Message",
			"message M {",
			"  // Field",
			"  string value = 1;",
			"}",
			"// enum",
			"enum E { ZERO = 0; }",
			"// Service",
			"service Other {",
			"  // Method",
			"  rpc Call (Req) returns (Res);",
			"}",
		].join("\n");
		const result = blocks(text, ".proto");
		expect(result.blocks.map(({ doc }) => doc)).toEqual([
			false,
			true,
			true,
			true,
			true,
			true,
		]);
	});

	test("single-quoted braces do not end a proto message scope", () => {
		const result = blocks(
			"message M {\n  string value = 1 [default = '}'];\n  // Field\n  string next = 2;\n}",
			".proto",
		);
		expect(result.blocks.map(({ doc }) => doc)).toEqual([true]);
	});

	test("proto blank lines detach comment blocks", () => {
		const text =
			'// header\n\nsyntax = "proto3";\n\n// detached\n\nmessage M {}';
		expect(blocks(text, ".proto").blocks.map(({ doc }) => doc)).toEqual([
			false,
			false,
		]);
	});

	test("comment markers inside proto strings are not comments", () => {
		const result = blocks(
			'option java_package = "example//path";\n// note\nmessage M {}',
			".proto",
		);
		expect(result.blocks).toHaveLength(1);
		expect(result.blocks[0]?.doc).toBe(true);
	});

	test("unknown extensions return null", () => {
		expect(commentBlocks("// note", ".unknown")).toBeNull();
	});

	test("cgo files are ambiguous for both import forms", () => {
		for (const text of [
			'package p\n/* preamble */\nimport "C"',
			'package p\nimport (\n\t"fmt"\n\t"C"\n)',
		]) {
			expect(isCgo(text)).toBe(true);
			expect(blocks(text)).toMatchObject({ ambiguous: true });
		}
	});

	test("non-cgo imports do not classify as cgo", () => {
		expect(isCgo('package p\nimport "fmt"')).toBe(false);
		expect(isCgo('package p\nimport (\n"fmt"\n)')).toBe(false);
	});
	test("generated headers exempt all comment blocks when found in the first five lines", () => {
		const result = blocks(
			"// Code generated by tool. DO NOT EDIT.\npackage p\n// ordinary note\nconst value = 1\n// trailing note",
		);
		expect(result.blocks.map(({ doc }) => doc)).toEqual([true, true, true]);
	});

	test("@generated in the first five lines exempts separated comment runs", () => {
		const result = commentBlocks(
			"// @generated\nconst value = 1;\n// generated prose",
			".ts",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([true, true]);
	});
	test("a generated marker on line five exempts comment blocks", () => {
		const result = commentBlocks(
			"const a = 1;\nconst b = 2;\nconst c = 3;\nconst d = 4;\n// @generated\n// ordinary note",
			".ts",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([true]);
	});

	test("a generated marker after line five does not exempt comment blocks", () => {
		const result = commentBlocks(
			"const a = 1;\nconst b = 2;\nconst c = 3;\nconst d = 4;\nconst e = 5;\n// @generated\n// ordinary note",
			".ts",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([false]);
	});

	test("generated marker inside a code string does not exempt the file", () => {
		const result = commentBlocks(
			'const m = "@generated";\n// ordinary note',
			".ts",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([false]);
	});
	test("Rust @generated directive exempts the file", () => {
		const result = commentBlocks(
			"// @generated by tool\nfn value() {}\n// ordinary note",
			".rs",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([true]);
	});
	test("Rust generated marker text inside a string is not a directive", () => {
		const result = commentBlocks(
			'const MESSAGE: &str = "// @generated";\n// ordinary note',
			".rs",
		);
		expect(result?.blocks.map(({ doc }) => doc)).toEqual([false]);
	});
	test("CRLF YAML blocks retain their one-based source line numbers", () => {
		const result = commentBlocks(
			"script: |\r\n  # payload\r\n  text\r\n# note\r\nnext: 1",
			".yaml",
		);
		expect(result?.blocks.map(({ start, lines }) => [start, lines])).toEqual([
			[4, 1],
		]);
	});
});
