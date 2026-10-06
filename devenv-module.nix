{ toolchain ? null, runtimePkgs ? null }:
{ pkgs, ... }:
let
  system = pkgs.stdenv.hostPlatform.system;
  selected =
    if toolchain == null then
      {
        rumdl = pkgs.rumdl;
        biome = pkgs.biome;
        rumdlBaseConfig = import ./rumdl-base-config.nix { inherit pkgs; };
      }
    else
      {
        inherit (toolchain.${system}) rumdl biome;
        rumdlBaseConfig = toolchain.${system}."rumdl-base-config";
      };
  runtimes = if runtimePkgs == null then pkgs else runtimePkgs.${system};
in
{
  packages = [
    selected.rumdl
    selected.biome
    runtimes.bun
    runtimes.nodejs
    runtimes.moon
  ];
  env.RUMDL_BASE_CONFIG = "${selected.rumdlBaseConfig}/rumdl/base.toml";
}
