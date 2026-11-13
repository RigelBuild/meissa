# meissa

Shared public tooling for RigelBuild repositories. Meissa pins the toolchain and exports rumdl, Biome, and their shared Markdown policy.

## Exports

The flake exports `packages.<system>.rumdl`, `packages.<system>.biome`, and `packages.<system>.rumdl-base-config`. The last package contains `rumdl/base.toml`. It also exports `devenvModules.default`, which adds only rumdl and Biome to PATH and sets `RUMDL_BASE_CONFIG` to that store file.

The opt-in `devenvModules.go` module adds golangci-lint, govulncheck, go-licenses, nilaway, and the shared Go tools including `code-shape-gate`. Set `meissa.go.toolchain` to the Go package used by the project; the analyzers are rebuilt with that toolchain.

```nix
{ inputs, pkgs, ... }:
{
  imports = [ inputs.meissa.devenvModules.go ];
  meissa.go.toolchain = inputs.meissa.packages.${pkgs.stdenv.hostPlatform.system}.go;
}
```

Consumers must use a Go toolchain that produces matching vendor output for the pinned Go analysis tools.

Import the module in a consumer's `devenv.nix`:

```nix
{ inputs, ... }:
{
  imports = [ inputs.meissa.devenvModules.default ];
}
```

Declare Meissa as a devenv input without following the consumer's nixpkgs pin:

```yaml
inputs:
  meissa:
    url: github:RigelBuild/meissa
```

Consumer `.rumdl.toml` files inherit the shared rules and can add local exclusions:

```toml
extends = "$RUMDL_BASE_CONFIG"
```

The policy is the shared `[MD0nn]` rule set from `rumdl/base.toml` in this repo. Meissa's own development shell also provides Bun, Node.js, and moon from its pinned nixpkgs.

## Code shape gate

`code-shape-gate` checks tracked source-file line counts and direct tracked-file counts per directory. Add `tools/code-shape-gate/config.cue` with `fileLimit`, `dirLimit`, counted `extensions`, and path `exclude` globs. The fixed limits are 500 physical lines per file and 10 direct files per directory, unless a directory matches a `dirLimits` exception. File counts omit tracked files under `testdata` or `fixtures`, generated headers in the first five lines, and configured excludes. Directory counts include every tracked file except configured excludes.

Optional `dirLimits` maps directory globs to `{limit: 50, reason: "record decisions together"}`. Limits must exceed 10 and reasons must contain at least three words. When multiple globs match, the smallest limit wins.

The gate also measures Go cognitive complexity, function lines and statements, and TypeScript cognitive complexity and function lines. The optional `functions` field uses the fixed Go limits 20, 60 and 40, and TypeScript limits 15 and 50. Existing configurations may omit it. Function collection requires `golangci-lint` and `biome` on PATH. The gate reports counted extensions without a collector on stderr.

Function allowlist entries are keyed by metric, path and function ID. Go IDs use function and receiver names, with closure ordinals within their enclosing function. TypeScript IDs use enclosing functions, classes and test titles where available. Suppression directives and mismatched Biome rule options are findings.

The gate runs Biome with its own generated configuration and the gate's limits. It copies the root Biome configuration's `javascript.parser` options. The repository's Biome rule settings are checked for consistency, but do not control measurement. The two measured rules may use `warn` or `error` levels in the repository configuration.

Skips are printed on stderr. A Go file outside the default build that contains functions, or a file Biome cannot parse, is an unmeasured finding. If the TypeScript function collector cannot parse a file and Biome reports an over-limit function, the gate reports a file-level finding. Existing function allowlist entries for unmeasured files are preserved on `--write`. Skips that leave no functions unmeasured are notices only. Use the gate's `exclude` to opt out.

Biome does not process files under `node_modules`. Exclude tracked paths in those directories from the gate.

Findings use the format `<path> <metric> value=<value> limit=<limit> fix=<cause>`. An `unmeasured` finding uses value and limit 0; its fix describes the cause.

```cue
functions: "go-lines": "src/example.go": [{id: "Example", value: 70, reason: "split by concern"}]
```

Add `tools/code-shape-gate/allowlist.cue` with `files` and `dirs` maps. Each entry has a measured `value` and a `reason` of at least three words:

```cue
files: "src/large.go": {value: 501, reason: "split by package concern"}
dirs: "src/tests": {value: 11, reason: "split test suites"}
```

Run `code-shape-gate <repo-root>` to check. It returns 1 for findings and 2 for configuration or execution errors. Set `GATE_BASE` to a Git ref to reject new or raised allowlist entries after the initial rollout. `code-shape-gate --write <repo-root>` removes cleared entries and lowers recorded values; it refuses additions or increases. Use `code-shape-gate --write --seed <repo-root>` only to seed initial rollout entries.

## Length gate

`length-gate` ratchets comment block length and document prose length over a Git diff. Add `length-gate.json` with scanned `extensions`, path `exclude` globs, and `docs` classes. Each class has a `name`, `include` and `exclude` globs, a prose-word `target`, and `markerAllowed`. Globs are validated when the config loads, so a malformed pattern fails the run before any file is judged. The first class whose `include` matches a file and whose `exclude` does not owns it.

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

Run `length-gate --config length-gate.json [--all] [--skip-class <name>]...`. `GATE_ROOT` selects the repository and defaults to the current Git root. Set `LENGTH_GATE_EVENT=pull_request` for a pull request check. It needs `LENGTH_GATE_BASE`, a `LENGTH_GATE_FETCH` branch to fetch from origin, or `LENGTH_GATE_MERGE=1`, and it reads the head from `LENGTH_GATE_HEAD` (default `HEAD`). `LENGTH_GATE_MERGE=1` treats the head as a pull request merge ref and uses its first parent as the base. A variable that is set counts as set even when empty. `--all` checks every configured file at the head, minus excludes, for any event. `--skip-class` is allowed only with `--all`.

The gate exits 0 when it passes or skips the event, 1 for findings, and 2 for a usage, configuration or Git error. The former npm library API is not part of the Go tool.

## Renovate preset

Consumers extend `github>RigelBuild/meissa//tools/renovate/preset.json5`.

## Packages

Packages under `packages/` are published to npm as `@rigelbuild/<tool>` and versioned independently. This includes `@rigelbuild/tsconfig` and `@rigelbuild/biome-config` for shared TypeScript and Biome configuration.

Meissa is public: never name or cite a private repository. `ref-gate` enforces this with `ref-gate.config.json`.

The Go `ref-gate` rejects the JavaScript-supported `v` regular-expression flag.

## Releases

Release Please opens one release pull request covering every changed package. When it merges, the release workflow publishes each released package to npm with provenance through trusted publishing. A new package needs one manual first publish before its trusted publisher can be attached.

## Local development

Run `direnv allow .` once, then `direnv exec . rumdl check .`. Run the length-gate tests with `go -C go test ./internal/lengthgate/...`; `direnv exec . bun test` still runs the Bun workspace tests. CI runs `nix flake check` and `moon run :ci` (lint, markdown, the ref-gate self-check, and every package's typecheck and tests).
