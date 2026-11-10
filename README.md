# meissa

Shared public tooling for RigelBuild repositories. Meissa pins the toolchain and exports rumdl, Biome, and their shared Markdown policy.

## Exports

The flake exports `packages.<system>.rumdl`, `packages.<system>.biome`, and `packages.<system>.rumdl-base-config`. The last package contains `rumdl/base.toml`. It also exports `devenvModules.default`, which adds only rumdl and Biome to PATH and sets `RUMDL_BASE_CONFIG` to that store file.

The opt-in `devenvModules.go` module adds golangci-lint, govulncheck, go-licenses, nilaway, and the shared Go tools including `code-shape-gate`. Set `meissa.go.toolchain` to the Go package used by the project; the analyzers are rebuilt with that toolchain.

```nix
{ inputs, pkgs, ... }:
{
  imports = [ inputs.meissa.devenvModules.go ];
  meissa.go.toolchain = pkgs.go;
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

`code-shape-gate` checks tracked source-file line counts and direct tracked-file counts per directory. Add `tools/code-shape-gate/config.cue` with `fileLimit`, `dirLimit`, counted `extensions`, and path `exclude` globs. The default limits are 500 physical lines per file and 10 direct files per directory. File counts omit tracked files under `testdata` or `fixtures`, generated headers in the first five lines, and configured excludes. Directory counts include every tracked file except configured excludes.

Optional `dirLimits` maps directory globs to `{limit: 50, reason: "record decisions together"}`. Limits must exceed 10 and reasons must contain at least three words. When multiple globs match, the smallest limit wins.

Add `tools/code-shape-gate/allowlist.cue` with `files` and `dirs` maps. Each entry has a measured `value` and a `reason` of at least three words:

```cue
files: "src/large.go": {value: 501, reason: "split by package concern"}
dirs: "src/tests": {value: 11, reason: "split test suites"}
```

Run `code-shape-gate <repo-root>` to check. It returns 1 for findings and 2 for configuration or execution errors. Set `GATE_BASE` to a Git ref to reject new or raised allowlist entries after the initial rollout. `code-shape-gate --write <repo-root>` removes cleared entries and lowers recorded values; it refuses additions or increases. Use `code-shape-gate --write --seed <repo-root>` only to seed initial rollout entries.

## Renovate preset

Consumers extend `github>RigelBuild/meissa//tools/renovate/preset.json5`.

## Packages

Packages under `packages/` are published to npm as `@rigelbuild/<tool>` and versioned independently. This includes `@rigelbuild/tsconfig` and `@rigelbuild/biome-config` for shared TypeScript and Biome configuration.

Meissa is public: never name or cite a private repository. `ref-gate` enforces this with `ref-gate.config.json`.

The Go `ref-gate` rejects the JavaScript-supported `v` regular-expression flag.

## Releases

Release Please opens one release pull request covering every changed package. When it merges, the release workflow publishes each released package to npm with provenance through trusted publishing. A new package needs one manual first publish before its trusted publisher can be attached.

## Local development

Run `direnv allow .` once, then `direnv exec . rumdl check .` or `direnv exec . bun test`. CI runs `nix flake check` and `moon run :ci` (lint, markdown, the ref-gate self-check, and every package's typecheck and tests).
