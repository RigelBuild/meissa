import { describe, expect, test } from "bun:test";
import { scanText } from "./index.ts";

describe("shell residues", () => {
	test("# in quotes and command substitutions is payload", () => {
		expect(
			scanText("shell", "echo '# value' \"quoted # value\"").residues[0],
		).toBe("echo '# value' \"quoted # value\"");
		expect(
			scanText("shell", "echo \"$(printf %s '# value')\"").residues[0],
		).toContain("# value");
	});

	test("hashes open comments only at a shell word start", () => {
		expect(scanText("shell", "echo hi;# note").residues[0]).toBe("echo hi;");
		expect(scanText("shell", "echo hi#suffix").residues[0]).toBe(
			"echo hi#suffix",
		);
		const parameter = "$" + "{x#prefix}";
		expect(scanText("shell", `echo ${parameter}`).residues[0]).toBe(
			`echo ${parameter}`,
		);
	});

	test("heredoc body stays code and following comments resume", () => {
		const result = scanText(
			"shell",
			"cat <<EOF\n# payload\nEOF\n# note\necho done",
		);
		expect(result.residues[1]).toBe("# payload");
		expect(result.residues[3]).toBe("");
		expect(result.residues[4]).toBe("echo done");
	});

	test("quoted and tab-stripped heredoc delimiters close their bodies", () => {
		expect(
			scanText("shell", "cat <<'EOF'\n# payload\nEOF\n# note").residues[3],
		).toBe("");
		expect(
			scanText("shell", "cat <<-EOF\n# payload\n\tEOF\n# note").residues[3],
		).toBe("");
	});

	test("here strings do not open heredoc frames", () => {
		const source = ['read x <<<"$y"', "# a real comment", "echo after"].join(
			"\n",
		);
		const { residues } = scanText("shell", source);
		expect(residues[0]).toBe('read x <<<"$y"');
		expect(residues[1]).toBe("");
		expect(residues[2]).toBe("echo after");
	});

	test("arithmetic shifts do not open heredoc frames", () => {
		for (const expression of ["n=$(( a << b ))", "n=$(( a << 2 ))"]) {
			const source = [expression, "# a real comment", "echo after"].join("\n");
			const { residues } = scanText("shell", source);
			expect(residues[0]).toBe(expression);
			expect(residues[1]).toBe("");
			expect(residues[2]).toBe("echo after");
		}
	});
});

describe("SQL residues", () => {
	test("hash-like tokens in strings and positional parameters are code", () => {
		expect(scanText("sql", "SELECT 'a -- b'; -- note").residues[0]).toBe(
			"SELECT 'a -- b';",
		);
		expect(
			scanText("sql", "SELECT * FROM t WHERE id = $1; -- note").residues[0],
		).toBe("SELECT * FROM t WHERE id = $1;");
	});

	test("doubled quotes and nested block comments are handled", () => {
		expect(
			scanText("sql", "SELECT 'it''s -- payload'; -- note").residues[0],
		).toBe("SELECT 'it''s -- payload';");
		expect(
			scanText(
				"sql",
				"/* outer /* inner */ still comment */ SELECT 1;",
			).residues[0]?.trim(),
		).toBe("SELECT 1;");
	});

	test("tagged dollar quotes keep their body and close on matching tag", () => {
		const result = scanText(
			"sql",
			"DO $fn$\n-- payload\nSELECT $$ not close $$;\n$fn$;\n-- note",
		);
		expect(result.residues[1]).toBe("-- payload");
		expect(result.residues[4]).toBe("");
	});
});
