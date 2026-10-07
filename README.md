# meissa

Shared public tooling for RigelBuild repositories. Meissa pins the toolchain and exports rumdl, Biome, and their shared Markdown policy.

## Exports

The flake exports `packages.<system>.rumdl`, `packages.<system>.biome`, and `packages.<system>.rumdl-base-config`. The last package contains `rumdl/base.toml`. It also exports `devenvModules.default`, which adds only rumdl and Biome to PATH and sets `RUMDL_BASE_CONFIG` to that store file.

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

## Local development

Run `direnv allow .` once, then `direnv exec . rumdl check .` or `direnv exec . bun test`. The CI workflow builds the flake exports, runs `rumdl check .`, and checks inheritance with the exported binary.
