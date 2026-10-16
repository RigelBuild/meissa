export { type Segment, scanAstro, segmentAstro } from "./astro.ts";
export {
	type CommentBlock,
	commentBlocks,
	isCgo,
} from "./comment-blocks.ts";
export {
	type DiffLine,
	extOf,
	type FileDiff,
	type LanguageId,
	parseDiff,
	type Residues,
	residueScannerFor,
	scanText,
} from "./diff.ts";
export { scanJsx } from "./jsx.ts";
export {
	compareMarkdown,
	type MarkdownItem,
	type MarkdownScan,
	markdownFingerprint,
	markdownScan,
} from "./markdown.ts";
export type { LangConfig } from "./scan.ts";
