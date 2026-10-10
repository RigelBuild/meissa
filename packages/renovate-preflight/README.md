# @rigelbuild/renovate-preflight

Checks that the Renovate GitHub App token can access the repository before
Renovate starts. It reports an actionable failure instead of Renovate's opaque
authentication error.

## Run

```sh
bunx @rigelbuild/renovate-preflight
```

Set these before running:

- `REPO`: `owner/name`.
- `RENOVATE_TOKEN`: the token Renovate authenticates with.
- `GH_TOKEN`: the same token. Without it, `gh` uses your own login and the probe does not test the App token.

`gh` must be on `PATH`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The token authenticates and can see the repository. |
| 1 | Preflight failed: no token, bad credentials, no access, or an unknown error. |
| 2 | Could not evaluate: `REPO` is missing, or the probe threw. |

## Library

`classify(probe: ProbeResult): PreflightResult` is exported, so a caller can
classify a probe result without running the CLI.
