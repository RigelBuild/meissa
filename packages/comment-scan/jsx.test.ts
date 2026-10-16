import { describe, expect, test } from "bun:test";
import { scanJsx, scanText } from "./index.ts";

describe("scanJsx", () => {
	test("a // inside element text stays in the residue", () => {
		expect(scanJsx("const E = () => <a>x//y</a>; // real").residues[0]).toBe(
			"const E = () => <a>x//y</a>;",
		);
	});

	test("element text beginning with // is not stripped", () => {
		expect(scanJsx("const E = () => <a>//lead</a>;").residues[0]).toBe(
			"const E = () => <a>//lead</a>;",
		);
	});

	test("block comment markers in JSX text are not stripped", () => {
		expect(
			scanJsx("const E = (y) => <a>{y}/* not */ text</a>;").residues[0],
		).toBe("const E = (y) => <a>{y}/* not */ text</a>;");
	});

	test("JSX expression comments are stripped", () => {
		expect(
			scanJsx("const E = () => <div>{/* inner */}a</div>;").residues[0],
		).toBe("const E = () => <div>{}a</div>;");
	});

	test("trailing comments are stripped after JSX", () => {
		expect(scanJsx("const E = () => <a>text</a>; // note").residues[0]).toBe(
			"const E = () => <a>text</a>;",
		);
	});

	test("generic arrows are not treated as JSX", () => {
		expect(scanJsx("const f = <T,>(x: T) => x; // note").residues[0]).toBe(
			"const f = <T,>(x: T) => x;",
		);
	});

	test("regex literals containing */ remain intact", () => {
		const result = scanJsx('block.replace(/\\s*\\n\\s*/g, " "); // note');
		expect(result.ambiguous).toBe(false);
		expect(result.residues[0]).toBe('block.replace(/\\s*\\n\\s*/g, " ");');
	});

	test("template literal hashes remain content", () => {
		const result = scanJsx("const s = `\n  # in shell\n`; // real\nx();");
		expect(result.residues[1]).toBe("  # in shell");
		expect(result.residues[2]).toBe("`;");
	});

	test("TypeScript line directives stay in the residue", () => {
		expect(scanJsx("// @ts-expect-error legacy shape").residues[0]).toBe(
			"// @ts-expect-error legacy shape",
		);
	});

	test("block-form Biome directives stay in the residue", () => {
		const text = [
			"const E = () => (",
			"  /* biome-ignore lint/a11y/useSemanticElements: reason */",
			'  <div role="button">x</div>',
			");",
		].join("\n");
		expect(scanJsx(text).residues[1]?.trim()).toBe(
			"/* biome-ignore lint/a11y/useSemanticElements: reason */",
		);
	});

	test("ordinary block comments are removed", () => {
		expect(scanJsx("/* prose */ const n = 1;").residues[0]?.trim()).toBe(
			"const n = 1;",
		);
	});

	test("comment width does not change code residue", () => {
		const wide = scanJsx("const n = f(/* a longer note */ 1);");
		const short = scanJsx("const n = f(/* note */ 1);");
		expect(wide.residues[0]).toBe(short.residues[0]);
	});

	test("multiline comments preserve source line numbering", () => {
		const result = scanJsx("/*\n * note\n */\nconst n = 1;");
		expect(result.residues).toHaveLength(4);
		expect(result.residues[3]).toBe("const n = 1;");
	});

	test("a parse error is ambiguous", () => {
		expect(scanJsx("const E = () => <a>unclosed;").ambiguous).toBe(true);
	});

	test("scanText routes TSX through the parser", () => {
		expect(scanText("tsx", "const E = () => <a>x//y</a>;").residues[0]).toBe(
			"const E = () => <a>x//y</a>;",
		);
	});
});
