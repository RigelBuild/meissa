# Build the analyzers with the consumer's Go toolchain; a different Go version can reject its stdlib/export data.
{ pkgs, goToolchain }:
let
  pins = import ./go-analysis-pins.nix;
  buildGoModule' = pkgs.buildGoModule.override { go = goToolchain; };

  rebuild =
    { pkg, builderArg, pin ? null, passGo ? false }:
    assert pin == null || (pin ? hash && (pin ? tag || pin ? rev));
    let
      base = pkg.override (
        { ${builderArg} = buildGoModule'; }
        // pkgs.lib.optionalAttrs passGo { go = goToolchain; }
      );
    in
    if pin == null then
      base
    else
      base.overrideAttrs (_old: {
        inherit (pin) version vendorHash;
        src = pkgs.fetchFromGitHub ({
          inherit (pin) owner repo hash;
        } // (if pin ? tag then { inherit (pin) tag; } else { inherit (pin) rev; }));
      });

  goVersionedBuilderArg =
    pkg:
    let
      args = builtins.filter (n: builtins.match "buildGo[0-9]+Module" n != null) (
        builtins.attrNames (pkgs.lib.functionArgs pkg.override)
      );
    in
    assert pkgs.lib.assertMsg (builtins.length args == 1)
      "${pkg.pname}: expected exactly one buildGo<N>Module arg, found ${builtins.toJSON args}";
    builtins.head args;
in
{
  golangci-lint = rebuild {
    pkg = pkgs.golangci-lint;
    builderArg = goVersionedBuilderArg pkgs.golangci-lint;
    pin = pins.golangci-lint;
  };
  govulncheck = rebuild {
    pkg = pkgs.govulncheck;
    builderArg = "buildGoLatestModule";
  };
  go-licenses = rebuild {
    pkg = pkgs.go-licenses;
    builderArg = "buildGoModule";
    passGo = true;
  };
  nilaway = (rebuild {
    pkg = pkgs.nilaway;
    builderArg = "buildGoModule";
    pin = pins.nilaway;
  }).overrideAttrs { doCheck = false; };
}
