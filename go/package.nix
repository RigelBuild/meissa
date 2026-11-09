{ buildGo127Module, lib }:
buildGo127Module {
  pname = "meissa-tools";
  version = (lib.importJSON ../.release-please-manifest.json).go or "0.0.0";
  src = lib.fileset.toSource { root = ./.; fileset = ./.; };
  vendorHash = "sha256-iWmjKlhEnBNvHgBC+VMb+SJi4lcP9AcL2zodMOiFhPw=";
  subPackages = [ "cmd/ref-gate" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  doCheck = false;
}
