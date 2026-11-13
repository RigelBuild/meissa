{ buildGoModule, lib }:
buildGoModule {
  pname = "meissa-tools";
  version = (lib.importJSON ../.release-please-manifest.json).go or "0.0.0";
  src = lib.fileset.toSource { root = ./.; fileset = ./.; };
  vendorHash = "sha256-hwYLM/VsoAPukwf35tvFSZnTzsuim3VV36Sm1O+x4fk=";
  subPackages = [ "cmd/code-shape-gate" "cmd/ref-gate" "cmd/refresh-nix-hashes" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  doCheck = false;
}
