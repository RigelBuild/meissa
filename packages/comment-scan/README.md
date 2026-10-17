# @rigelbuild/comment-scan

Language-aware source comment scanning, diff parsing, and Markdown structure fingerprinting.

## API

```ts
import { commentBlocks, scanText } from "@rigelbuild/comment-scan";

commentBlocks("// note\nconst value = 1;", ".ts");
scanText("ts", "const value = 1; // note");
```

`commentBlocks` returns comment runs with 1-based start lines, raw first lines, and doc-comment classification. It returns `null` for unsupported extensions and marks scanner failures as ambiguous.

`scanText` returns one code residue per source line. TypeScript-backed JSX and Astro parsing is included as a runtime dependency.

Go declaration comments are classified only at package scope. Parser-backed JSX and Astro scans normalize CRLF input before parsing.
