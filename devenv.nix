{ pkgs, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default flake.devenvModules.go ];
  meissa.go.toolchain = pkgs.go_1_27;
  packages = [ pkgs.go_1_27 pkgs.bun pkgs.nodejs pkgs.moon packages.tools ];
  env.RUMDL_BIN = "${packages.rumdl}/bin/rumdl";
  # A leftover ~/.proto makes moon prepend shims that exec a missing proto; use the nix tools.
  env.MOON_TOOLCHAIN_FORCE_GLOBALS = "true";
}
