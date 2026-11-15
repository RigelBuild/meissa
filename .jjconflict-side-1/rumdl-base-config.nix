{ pkgs }:
pkgs.runCommand "rumdl-base-config" { } ''
  mkdir -p "$out/rumdl"
  cp ${./rumdl/base.toml} "$out/rumdl/base.toml"
''
