# Pins consumed by tools/toolchain/go-analysis.nix. The pinned sources supply Go
# language/IR compatibility that the nixpkgs versions do not yet provide.
{
  nilaway = {
    version = "0-unstable-2026-09-18";
    owner = "uber-go";
    repo = "nilaway";
    rev = "acb8859b9031bb9496be97e027df5573f9fb5340";
    hash = "sha256-GvDZ5tlvOrTI93tYcIcLd45ZHdqwFopVtoBffD/kbuM=";
    vendorHash = "sha256-qVmvDneq6V/q5UHZ/Cjjqd5/XPPNfvVGoxwg9nz4/Ds=";
  };
  golangci-lint = {
    version = "2.15.0";
    owner = "golangci";
    repo = "golangci-lint";
    tag = "v2.15.0";
    hash = "sha256-m9PoJHescRuT9DiKsb6LL+lQlc7RKAWDiRyf8pNMffA=";
    vendorHash = "sha256-gWbhZc/BJwuiwyTMaR1ShEBI1lo0bGLCXsdj5n91baw=";
  };
}
