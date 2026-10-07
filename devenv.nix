{ pkgs, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default ];
  packages = [ pkgs.bun pkgs.nodejs pkgs.moon ];
  env.RUMDL_BIN = "${packages.rumdl}/bin/rumdl";
}
