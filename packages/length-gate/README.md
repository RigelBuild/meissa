# @rigelbuild/length-gate

Ratchets comment and document length in Git commits.

## CLI

```sh
length-gate --config length-gate.json [--all] [--skip-class <name>]...
```

Config has scanned extensions, excluded path globs, and document classes:

```json
{
  "extensions": [".ts"],
  "exclude": ["vendor/**"],
  "docs": [
    {
      "name": "spec",
      "include": ["docs/specs/**/*.md"],
      "exclude": [],
      "target": 1500,
      "markerAllowed": false
    }
  ]
}
```

`GATE_ROOT` selects the repository (default: current Git root). Set `LENGTH_GATE_EVENT=pull_request` for a PR check; it needs `LENGTH_GATE_BASE` or shallow-safe `LENGTH_GATE_FETCH`, and uses `LENGTH_GATE_HEAD` (default `HEAD`). Set `LENGTH_GATE_MERGE=1` when the head is a merge ref. `--all` scans every file at the head for any event; `--skip-class` is allowed only with `--all`.

Exit codes: `0` pass or skipped event, `1` findings, `2` usage, config, or Git error.

## API

```ts
import { judgeFile, proseWords } from "@rigelbuild/length-gate";

judgeFile(config, fileDiff, { before, after });
proseWords(markdown, true);
```
