import { describe, expect, test } from "bun:test";
import { scanAstro, segmentAstro } from "./index.ts";

const PAGE = [
	"---",
	"// a note above the title",
	'const title = "Home";',
	"---",
	"<!-- a note about the header -->",
	"<h1>{title}</h1>",
	"<p>see http://example.com/a//b for details</p>",
	"{/* a JSX-style note */}",
	"<style>",
	"  /* a note about the rule */",
	"  h1 { color: red; }",
	"</style>",
	"<script>",
	"  // a note about the handler",
	'  document.querySelector("h1");',
	"</script>",
].join("\n");

describe("segmentAstro", () => {
	test("splits frontmatter, template and islands", () => {
		const segments = segmentAstro(PAGE);
		if (segments === null) throw new Error("expected segments");
		expect(segments.map((segment) => segment.lang.id)).toEqual([
			"html",
			"ts",
			"html",
			"html",
			"css",
			"html",
			"ts",
			"html",
		]);
	});

	test("an island tag sharing a line with body text is unmodelled", () => {
		expect(segmentAstro("<script>alert(1)\n</script>")).toBeNull();
	});

	test("an unclosed frontmatter fence is unmodelled", () => {
		expect(segmentAstro("---\nconst value = 1;")).toBeNull();
	});

	test("one-line empty islands are template text", () => {
		expect(
			segmentAstro("<script is:inline set:html={json}></script>")?.map(
				(segment) => segment.lang.id,
			),
		).toEqual(["html"]);
	});

	test("script mentions in HTML comments are prose", () => {
		const text = "<!-- <script> example -->\n<p>after</p>";
		expect(segmentAstro(text)?.map((segment) => segment.lang.id)).toEqual([
			"html",
		]);
		expect(scanAstro(text).ambiguous).toBe(false);
	});

	test("an unclosed island is unmodelled", () => {
		expect(segmentAstro("<style>\na { color: red; }")).toBeNull();
	});

	test("a template ending inside an HTML comment is ambiguous", () => {
		expect(scanAstro("<p>x</p>\n<!-- unfinished").ambiguous).toBe(true);
	});

	test("script tags inside attribute strings do not open islands", () => {
		const text =
			'<p data-tpl="<script>alert(1)</script>">x</p>\n<!-- note -->\n<p>after</p>';
		expect(segmentAstro(text)?.map((segment) => segment.lang.id)).toEqual([
			"html",
		]);
		expect(scanAstro(text).residues[0]).toBe(
			'<p data-tpl="<script>alert(1)</script>">x</p>',
		);
	});

	test("an opening tag with a trailing comment still opens its island", () => {
		const text = "<script> <!-- note -->\n// comment\nrun();\n</script>";
		expect(segmentAstro(text)?.map((segment) => segment.lang.id)).toEqual([
			"html",
			"ts",
			"html",
		]);
		expect(scanAstro(text).residues[1]).toBe("");
	});

	test("a live open tag with trailing body text is unmodelled", () => {
		expect(segmentAstro("<div><script>alert(1)\n</script></div>")).toBeNull();
	});

	test("an island body left mid-construct is ambiguous", () => {
		expect(
			scanAstro("<style>\n/* unfinished\n</style>\n<p>after</p>").ambiguous,
		).toBe(true);
	});
});

describe("scanAstro", () => {
	test("strips comments while retaining code in every region", () => {
		const result = scanAstro(PAGE);
		expect(result.ambiguous).toBe(false);
		expect(result.residues[1]).toBe("");
		expect(result.residues[2]).toBe('const title = "Home";');
		expect(result.residues[4]).toBe("");
		expect(result.residues[5]).toBe("<h1>{title}</h1>");
		expect(result.residues[7]).toBe("{}");
		expect(result.residues[10]).toBe("  h1 { color: red; }");
		expect(result.residues[13]).toBe("");
		expect(result.residues[14]).toBe('  document.querySelector("h1");');
	});

	test("// in template prose is not a comment", () => {
		expect(scanAstro(PAGE).residues[6]).toBe(
			"<p>see http://example.com/a//b for details</p>",
		);
	});

	test("frontmatter fences and island tags remain in residue", () => {
		const result = scanAstro(PAGE);
		expect(result.residues[0]).toBe("---");
		expect(result.residues[3]).toBe("---");
		expect(result.residues[8]).toBe("<style>");
		expect(result.residues[11]).toBe("</style>");
		expect(result.residues[12]).toBe("<script>");
		expect(result.residues[15]).toBe("</script>");
	});

	test("an unmodelled shape is ambiguous", () => {
		expect(scanAstro("<script>alert(1)\n</script>").ambiguous).toBe(true);
	});

	test("tag-like text and braces in strings do not misroute markup", () => {
		expect(scanAstro("<p>{`literal <span> and { braces`}</p>").ambiguous).toBe(
			false,
		);
	});

	test("markup expressions preserve JSX text", () => {
		const text =
			"---\nconst items = [];\n---\n<ul>\n{items.map((item) => <a href={item.url}>see http://x//y</a>)}\n</ul>\n<p>after</p>";
		const result = scanAstro(text);
		expect(result.ambiguous).toBe(false);
		expect(result.residues[4]).toBe(
			"{items.map((item) => <a href={item.url}>see http://x//y</a>)}",
		);
	});

	test("multiline markup expressions preserve source line count", () => {
		const text =
			"---\nconst items = [];\n---\n<ul>\n  {items.map((item) => (\n    <a href={item.url}>see http://x//y for details</a>\n  ))}\n</ul>\n<p>after</p>";
		const result = scanAstro(text);
		expect(result.ambiguous).toBe(false);
		expect(result.residues).toHaveLength(9);
		expect(result.residues[5]).toBe(
			"    <a href={item.url}>see http://x//y for details</a>",
		);
	});

	test("comments inside multiline markup expressions are stripped", () => {
		const text =
			"<ul>\n  {items.map((item) => (\n    /* note */\n    <a href={item.url}>x//y</a>\n  ))}\n</ul>";
		const result = scanAstro(text);
		expect(result.ambiguous).toBe(false);
		expect(result.residues[2]).toBe("");
		expect(result.residues[3]).toBe("    <a href={item.url}>x//y</a>");
	});

	test("malformed multiline expressions are ambiguous", () => {
		expect(
			scanAstro("<ul>\n  {items.map((item) => (\n    <a>x</a>").ambiguous,
		).toBe(true);
	});

	test("template text after a markup expression's closing brace stays template", () => {
		const text =
			'<p>\n\t{url ? (\n\t\t<a href={url}>{label}</a>\n\t) : (\n\t\tlabel\n\t)}, or email // not a comment\n\t<a href="mailto:x">x</a>.\n</p>';
		const result = scanAstro(text);
		expect(result.ambiguous).toBe(false);
		expect(result.residues).toHaveLength(8);
		expect(result.residues[5]).toBe("\t)}, or email // not a comment");
	});

	test("a comment inside an expression with trailing text is still stripped", () => {
		const result = scanAstro("<p>\n{<a>x</a> /* note */}, or\n</p>");
		expect(result.ambiguous).toBe(false);
		expect(result.residues[1]).toBe("{<a>x</a> }, or");
	});

	test("plain brace expressions use the lexical scanner", () => {
		const result = scanAstro("<p>{title}</p>\n<p>after</p>");
		expect(result.ambiguous).toBe(false);
		expect(result.residues[0]).toBe("<p>{title}</p>");
	});
});

describe("scanAstro CRLF input", () => {
	test("matches LF residues and line count", () => {
		const crlf = scanAstro(PAGE.replace(/\n/g, "\r\n"));
		const lf = scanAstro(PAGE);
		expect(crlf.ambiguous).toBe(lf.ambiguous);
		expect(crlf.residues).toEqual(lf.residues);
		expect(crlf.residues).toHaveLength(PAGE.split("\n").length);
	});
});
