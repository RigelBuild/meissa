{ pkgs, config, ... }:
let
  flake = builtins.getFlake (toString ./.);
  packages = flake.packages.${pkgs.stdenv.hostPlatform.system};
in
{
  imports = [ flake.devenvModules.default flake.devenvModules.go ];
  meissa.go.toolchain = packages.go;
  packages = [ packages.go pkgs.bun pkgs.nodejs pkgs.moon packages.tools packages.hk ];
  env.RUMDL_BIN = "${packages.rumdl}/bin/rumdl";
  # A leftover ~/.proto makes moon prepend shims that exec a missing proto; use the nix tools.
  env.MOON_TOOLCHAIN_FORCE_GLOBALS = "true";
  env.HK_STATE_DIR = "${config.devenv.state}/hk";
}
