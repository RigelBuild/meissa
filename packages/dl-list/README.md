# @rigelbuild/dl-list

Lists design-ledger decisions for a consumer repository. It reads `designsRoot` and `surfaceDepth` from the ledger config, parses entries with the shared design-ledger-gate parser, then prints one line per decision.

## Usage

```sh
dl-list [--config design-ledger.config.json] [--surface S] [--area A] [--status active|superseded|retired] [--grep TEXT] [--glob GLOB]
```

`--config` defaults to `design-ledger.config.json` in the current working directory. The config determines the default decision-file glob: `designsRoot/*/decisions/*/DL-*.md` at surface depth 1, or `designsRoot/decisions/*/DL-*.md` at depth 0. `--glob` overrides that pattern. Filters combine. `--grep` is case-insensitive and searches the printed line.

Exit code `1` means a decision file is malformed or the selected glob matches no files. Exit code `2` means usage, config, or read error.
