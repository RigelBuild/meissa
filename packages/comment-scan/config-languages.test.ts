import { describe, expect, test } from "bun:test";
import { scanText } from "./index.ts";

describe("YAML residues", () => {
	test("# markers inside double- and single-quoted values stay code", () => {
		expect(scanText("yaml", 'value: "a#b" # note').residues[0]).toBe(
			'value: "a#b"',
		);
		expect(scanText("yaml", "value: 'a#b' # note").residues[0]).toBe(
			"value: 'a#b'",
		);
	});

	test("hashes in URL scalars and apostrophes are not comment starts", () => {
		expect(scanText("yaml", "url: http://a#f").residues[0]).toBe(
			"url: http://a#f",
		);
		expect(
			scanText("yaml", "text: it's fine\nnext: '# value'").residues[1],
		).toBe("next: '# value'");
	});

	test("block scalar bodies are payload and dedent resumes scanning", () => {
		const result = scanText(
			"yaml",
			"script: |\n  # payload\n  line\n# note\nnext: 1",
		);
		expect(result.residues[1]).toBe("  # payload");
		expect(result.residues[3]).toBe("");
		expect(result.residues[4]).toBe("next: 1");
	});

	test("tags and anchors before block scalar indicators are supported", () => {
		expect(
			scanText("yaml", "value: !!str |\n  # payload\nnext: 1").residues[1],
		).toBe("  # payload");
	});

	test("templated lines are not comments", () => {
		expect(scanText("yaml", "{{ if .Values.x }} # value").residues[0]).toBe(
			"{{ if .Values.x }} # value",
		);
	});
});

describe("TOML residues", () => {
	test("hash markers in basic and literal strings stay code", () => {
		expect(scanText("toml", 'value = "a#b" # note').residues[0]).toBe(
			'value = "a#b"',
		);
		expect(scanText("toml", "value = 'a#b' # note").residues[0]).toBe(
			"value = 'a#b'",
		);
	});

	test("multiline string payload stays code", () => {
		const result = scanText("toml", 'input = """\n# payload\n"""\n# note');
		expect(result.residues[1]).toBe("# payload");
		expect(result.residues[3]).toBe("");
	});
});
