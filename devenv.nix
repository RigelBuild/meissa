{ pkgs, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default flake.devenvModules.go ];
  meissa.go.toolchain = pkgs.go_1_27;
  packages = [ pkgs.go_1_27 pkgs.bun pkgs.nodejs pkgs.moon ];
  env.RUMDL_BIN = "${packages.rumdl}/bin/rumdl";
}
