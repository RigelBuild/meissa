{ config, lib, pkgs, ... }:
let
  goAnalysis = import ./tools/toolchain/go-analysis.nix {
    inherit pkgs;
    goToolchain = config.meissa.go.toolchain;
  };
in
{
  options.meissa.go.toolchain = lib.mkOption {
    type = lib.types.package;
    description = "Go toolchain used to build the Go analysis tools.";
  };

  config.packages = builtins.attrValues goAnalysis;
}
