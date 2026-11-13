{ pkgs }:
# nixpkgs still ships go 1.27.3; drop this once go_1_27 reaches 1.27.4 (security fixes).
pkgs.go_1_27.overrideAttrs (_old: rec {
  version = "1.27.4";
  src = pkgs.fetchurl {
    url = "https://go.dev/dl/go${version}.src.tar.gz";
    hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
  };
})
