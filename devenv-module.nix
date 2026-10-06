{ toolchain }:
{ pkgs, ... }:
let
  exports = toolchain.${pkgs.stdenv.hostPlatform.system};
in
{
  packages = [ exports.rumdl exports.biome ];
  env.RUMDL_BASE_CONFIG = "${exports."rumdl-base-config"}/rumdl/base.toml";
}
