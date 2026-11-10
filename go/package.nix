{ buildGo127Module, lib }:
buildGo127Module {
  pname = "meissa-tools";
  version = (lib.importJSON ../.release-please-manifest.json).go or "0.0.0";
  src = lib.fileset.toSource { root = ./.; fileset = ./.; };
  vendorHash = "sha256-gXUE5nvRY4WOvIP0ZM5WscVD6BCXFgjD0OQ+gx/XBFs=";
  subPackages = [ "cmd/code-shape-gate" "cmd/ref-gate" "cmd/refresh-nix-hashes" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  doCheck = false;
}
