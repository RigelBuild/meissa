{ buildGoModule, lib }:
buildGoModule {
  pname = "meissa-tools";
  version = (lib.importJSON ../.release-please-manifest.json).go or "0.0.0";
  src = lib.fileset.toSource { root = ./.; fileset = ./.; };
  vendorHash = "sha256-L5Z6mkY7c8f+wFMHE8gnQaaJlTkVi8ER2Nfw4IRtZrc=";
  subPackages = [ "cmd/code-shape-gate" "cmd/length-gate" "cmd/ref-gate" "cmd/refresh-nix-hashes" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  doCheck = false;
}
