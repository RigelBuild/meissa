# meissa

Shared public tooling for RigelBuild repositories. Meissa pins the toolchain and exports rumdl, Biome, and their shared Markdown policy.

## Exports

The flake exports `packages.<system>.rumdl`, `packages.<system>.biome`, and `packages.<system>.rumdl-base-config`. The last package contains `rumdl/base.toml`. It also exports `devenvModules.default`, which adds only rumdl and Biome to PATH and sets `RUMDL_BASE_CONFIG` to that store file.

The opt-in `devenvModules.go` module adds golangci-lint, govulncheck, go-licenses, and nilaway. Set `meissa.go.toolchain` to the Go package used by the project; the analyzers are rebuilt with that toolchain.

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

The policy is the shared `[MD0nn]` rule set in Compass's [`.rumdl.toml`](https://github.com/RigelBuild/compass/blob/main/.rumdl.toml). Meissa's own development shell also provides Bun, Node.js, and moon from its pinned nixpkgs.

## Renovate preset

Consumers extend `github>RigelBuild/meissa//tools/renovate/preset.json5`.

## Packages

Packages under `packages/` are published to npm as `@rigelbuild/<tool>` and versioned independently. This includes `@rigelbuild/tsconfig` and `@rigelbuild/biome-config` for shared TypeScript and Biome configuration.

Meissa is public: never name or cite a private repository. `ref-gate` enforces this with `ref-gate.config.json`.

## Releases

Release Please opens one release pull request covering every changed package. When it merges, the release workflow publishes each released package to npm with provenance through trusted publishing. A new package needs one manual first publish before its trusted publisher can be attached.

## Local development

Run `direnv allow .` once, then `direnv exec . rumdl check .` or `direnv exec . bun test`. CI runs `nix flake check` and `moon run :ci` (lint, markdown, the ref-gate self-check, and every package's typecheck and tests).
