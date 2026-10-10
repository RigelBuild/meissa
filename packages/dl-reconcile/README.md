# @rigelbuild/dl-reconcile

Reports the decision IDs present in the configured layout to the shared counter. The response lists updated and inserted claims, stale claims, and duplicate IDs.

## Usage

```sh
bun packages/dl-reconcile/index.ts --config ./ledger.config.json
bun packages/dl-reconcile/index.ts --config ./ledger.config.json --check
bun packages/dl-reconcile/index.ts --config ./ledger.config.json --stale-exit
```

The command discovers files according to the configured design layout, parses every decision, and refuses to submit an empty frontier. `--check` validates files and reports the number found without contacting the counter. `--stale-exit` exits 1 when stale claims remain.

## Credentials

`DL_CLAIM_TOKEN` supplies the bearer token. A non-empty trimmed `DL_CLAIM_TOKEN_FILE` value takes precedence. The counter URL is read from `counter.url` in the config; there is no environment override. `GATE_ROOT` optionally sets the repository root; otherwise the Git toplevel is used.

## Exit codes

- `0`: reconciliation succeeded, with no stale claims when `--stale-exit` is set.
- `1`: counter request or service failure, or stale claims with `--stale-exit`.
- `2`: usage, configuration, or read error.
