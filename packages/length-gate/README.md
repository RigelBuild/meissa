# @rigelbuild/length-gate

Pure comment and document length ratchet with `LENGTH` marker parsers.

## API

```ts
import { judgeFile, proseWords } from "@rigelbuild/length-gate";

judgeFile(config, fileDiff, { before, after });
proseWords(markdown, true);
```

The package has no I/O. It uses `@rigelbuild/comment-scan` for source comment blocks and `diff` for stable line pairing.
