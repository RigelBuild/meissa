# @rigelbuild/dl-claim

Claims decision IDs from the configured counter partition. The request repository, surface list, and counter URL come from the design-ledger configuration.

## Usage

```sh
bun packages/dl-claim/index.ts --config ./ledger.config.json --ref RIG-1234 --lane feature/decision
bun packages/dl-claim/index.ts --config ./ledger.config.json --ref none --lane feature/decision --count 3 --surface alpha
```

Name the branch before claiming IDs. Claims must use the PR head branch exactly; for a fork PR, the gate uses `<owner>:<branch>`. Move an existing claim with `--rebind`, or remove an abandoned claim with `--release`:

```sh
bun packages/dl-claim/index.ts --config ./ledger.config.json --rebind --from old/head --lane new/head --ids DL-377,DL-378
bun packages/dl-claim/index.ts --config ./ledger.config.json --release --lane old/head --ids DL-377
```

Both maintenance commands require `DL_CLAIM_TOKEN` or `DL_CLAIM_TOKEN_FILE`. A lane mismatch refuses the whole operation and reports the affected IDs.

`--ref` accepts `RIG-n` or `none`. `--lane` identifies the claims. `--count` is 1 to 10 and defaults to 1. `--surface` is required when the config lists multiple surfaces; a single configured surface is selected by default.

A failed request may have consumed IDs. Do not rerun it blindly. The error includes the configured counter URL and partition for checking the lane's claims.

## Credentials

`DL_CLAIM_TOKEN` supplies the bearer token. A non-empty trimmed `DL_CLAIM_TOKEN_FILE` value takes precedence. The counter URL is read from `counter.url` in the config; there is no environment override.

## Exit codes

- `0`: IDs claimed.
- `1`: counter request or service failure.
- `2`: usage, configuration, or read error.
