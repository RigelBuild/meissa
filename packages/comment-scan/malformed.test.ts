import { describe, expect, test } from "bun:test";
import { type LanguageId, scanText } from "./index.ts";

const unfinished: [string, LanguageId, string][] = [
	["Go block comment", "go", "/* unfinished"],
	["TypeScript template", "ts", "`unfinished ${ x"],
	["Rust block comment", "rust", "/* unfinished"],
	["Nix interpolation", "nix", '"${unfinished"'],
	["Python triple string", "python", '"""unfinished'],
	["TOML multiline string", "toml", 'value = """unfinished'],
	["YAML quoted scalar", "yaml", 'value: "unfinished'],
	["shell heredoc", "shell", "cat <<EOF\npayload"],
	["SQL dollar quote", "sql", "DO $$\npayload"],
	["KDL block comment", "kdl", "/* unfinished"],
	["CSS URL", "css", "src: url(/fonts/a.otf"],
	["HTML attribute value", "html", '<p data-x="unfinished'],
	["KDL raw string", "kdl", 'value #"unfinished'],
	["Proto block comment", "proto", "/* unfinished"],
	["Proto string", "proto", 'option java_package = "unfinished'],
];
describe("unclosed lexical constructs", () => {
	for (const [name, language, text] of unfinished) {
		test(`${name} is ambiguous`, () =>
			expect(scanText(language, text).ambiguous).toBe(true));
	}

	test("YAML block scalars may end at EOF", () => {
		expect(scanText("yaml", "script: |\n  echo value").ambiguous).toBe(false);
	});

	test("unterminated single-line strings are ambiguous", () => {
		for (const [language, source] of [
			["go", 'value = "unfinished'],
			["ts", 'const value = "unfinished'],
			["toml", "value = 'unfinished"],
			["sql", "SELECT 'unfinished"],
		] as const) {
			expect(scanText(language, source).ambiguous).toBe(true);
		}
	});

	test("closed strings and comments are not ambiguous", () => {
		expect(scanText("ts", 'const value = "closed";\n// note').ambiguous).toBe(
			false,
		);
		expect(scanText("rust", "/* closed */\nfn run() {}").ambiguous).toBe(false);
	});
});
